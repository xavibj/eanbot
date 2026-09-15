package crawler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Page is the outcome of processing a single URL: either it was fetched,
// blocked by robots.txt, or failed with a network/read error.
type Page struct {
	URL         string
	Depth       int
	Status      int    // 0 if no request was made (blocked or network error)
	ContentType string // media type only, lower-case, no parameters ("text/html")
	Size        int64
	Duration    time.Duration
	Title       string
	Description string
	Canonical   string // absolute, normalized; "" if none
	MetaRobots  string // raw <meta name="robots"> content
	NoIndex     bool
	NoFollow    bool // meta robots nofollow
	H1          string
	RedirectTo  string // absolute, normalized, if 3xx with Location
	Error       string // "" if ok; text of the network/read error
	Blocked     bool   // forbidden by robots.txt (no request was made)
	Links       []Link // outbound links (empty if not HTML)
	FetchedAt   time.Time
}

// Sink receives one Page for every URL processed (fetched, blocked, or
// errored).
type Sink interface {
	Page(p Page)
}

// Stats summarizes a finished (or cancelled) crawl.
type Stats struct {
	Fetched, Blocked, Errors int // Fetched counts any request made (any status code)
	Queued                   int // URLs left unvisited because of limits
	RobotsTxt                string
	RobotsError              string // why robots.txt could not be applied ("" when it was fetched)
	Sitemaps                 int    // URLs discovered via sitemaps
	FinalHost                string // effective scope host, after following any seed-level cross-host redirect chain
}

// Run executes a full crawl and returns its statistics. It finishes when
// the frontier drains, MaxPages is reached, or ctx is cancelled (in which
// case the wrapped ctx.Err() is returned together with partial Stats).
// Pages are delivered to Sink in the order in which they finish; with
// Concurrency == 1 that order is deterministic BFS order.
func Run(ctx context.Context, cfg Config, f Fetcher, s Sink) (Stats, error) {
	cfg = cfg.WithDefaults()
	if errs := cfg.Validate(); len(errs) > 0 {
		return Stats{}, errors.New(strings.Join(errs, "; "))
	}

	normalizedSeed, err := Normalize(cfg.Seed, nil)
	if err != nil {
		return Stats{}, fmt.Errorf("crawler: invalid seed: %w", err)
	}
	su, err := url.Parse(normalizedSeed)
	if err != nil {
		return Stats{}, fmt.Errorf("crawler: invalid seed: %w", err)
	}
	seedHost := su.Hostname()

	fr := NewFrontier()

	var stats Stats
	robots := resolveRobots(ctx, cfg, f, su, &stats)

	delay := cfg.Delay
	if rd := robots.CrawlDelay(cfg.RobotsToken); rd > delay {
		delay = rd
	}

	limiter := &rateLimiter{delay: delay}

	// Resolve the seed (and, if it redirects to another host, the chain of
	// depth-0 redirects that follows it). This determines the crawl's
	// effective scope before sitemaps or the concurrent worker pool ever
	// run, per specs/002-crawler.md ("Redirecciones").
	chain := resolveSeedChain(ctx, cfg, f, s, normalizedSeed, seedHost, su, robots, limiter, fr, &stats)

	stats.FinalHost = chain.host

	collectSitemaps(ctx, cfg, f, chain.robots, chain.base, chain.host, fr, &stats)

	state := &engineState{fr: fr, fetched: chain.fetched, blocked: chain.blocked, errs: chain.errs}

	var wg sync.WaitGroup
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runWorker(ctx, cfg, f, s, chain.robots, chain.host, state, limiter)
		}()
	}
	wg.Wait()

	stats.Fetched = state.fetched
	stats.Blocked = state.blocked
	stats.Errors = state.errs
	stats.Queued = fr.Len()

	if ctx.Err() != nil {
		return stats, fmt.Errorf("crawler: crawl cancelled: %w", ctx.Err())
	}
	return stats, nil
}

// maxSeedHostHops bounds how many times the seed's own redirect chain may
// switch to a different host before the crawler gives up following it.
const maxSeedHostHops = 5

// seedChainResult carries the outcome of resolving the seed's depth-0
// redirect chain: the effective scope (host, robots, courtesy delay and a
// base URL for resolving relative sitemap locations) plus every counter it
// consumed.
type seedChainResult struct {
	host    string
	robots  *Robots
	base    *url.URL
	fetched int
	blocked int
	errs    int
}

// resolveSeedChain fetches the seed URL and follows any depth-0 redirect
// chain from it, per specs/002-crawler.md:
//
//   - A same-host redirect is followed within the same scope.
//   - A cross-host redirect makes the crawler adopt the new host as its
//     scope (seedHost), re-fetch that host's robots.txt (same 2xx/4xx/5xx
//     rules, skipped entirely with IgnoreRobots), and continue the chain
//     from there — up to maxSeedHostHops host switches. Once the limit is
//     reached, the redirect is recorded but not followed.
//
// Every hop (including intermediate 3xx responses) is delivered to s as a
// normal Page and counted in the returned fetched/blocked/errs counters.
// Once the chain lands on a non-redirect page (or errors, is blocked, or
// hits the hop limit), that page's outbound links are pushed into fr at
// depth 1, exactly as the main engine would for any other page.
func resolveSeedChain(ctx context.Context, cfg Config, f Fetcher, s Sink, seedURL, seedHost string, su *url.URL, robots *Robots, limiter *rateLimiter, fr *Frontier, stats *Stats) seedChainResult {
	currentURL := seedURL
	currentHost := seedHost
	currentRobots := robots
	currentBase := su
	fetched, blocked, errs, hops := 0, 0, 0, 0

	result := func() seedChainResult {
		return seedChainResult{host: currentHost, robots: currentRobots, base: currentBase, fetched: fetched, blocked: blocked, errs: errs}
	}

	for {
		// Mirrors runWorker's gate: MaxPages is checked before anything
		// else, including the robots.txt check, so a URL that cannot be
		// fetched within budget is left queued (via fr.Push, which also
		// marks it seen) rather than silently dropped.
		if fetched >= cfg.MaxPages {
			fr.Push(currentURL, 0)
			return result()
		}

		if !currentRobots.Allowed(cfg.RobotsToken, robotsPath(currentURL)) {
			blocked++
			fr.seen[currentURL] = true
			s.Page(Page{URL: currentURL, Depth: 0, Blocked: true, Error: currentRobots.BlockReason(), FetchedAt: time.Now()})
			return result()
		}

		page := fetchAndBuildPage(ctx, cfg, f, currentHost, currentURL, 0, limiter)
		fetched++
		// Every hop is fetched directly here, bypassing the frontier's own
		// Push/Pop bookkeeping, so it must be marked seen by hand: a later
		// page linking back to any URL in this chain (very often the seed
		// itself) must not cause it to be queued and fetched again.
		fr.seen[currentURL] = true

		if page.Error != "" {
			errs++
			s.Page(page)
			return result()
		}

		if page.RedirectTo == "" {
			// Not a redirect (or a redirect with no usable Location): the
			// chain ends here.
			s.Page(page)
			enqueueLinks(page, cfg, currentHost, fr)
			return result()
		}

		target := page.RedirectTo
		tu, err := url.Parse(target)
		if err != nil {
			s.Page(page)
			return result()
		}

		if fr.seen[target] {
			// A redirect loop (or a redirect back to a URL already
			// resolved elsewhere in the chain): stop following it, but
			// the loop itself is not an error worth reporting beyond the
			// page already recorded above.
			s.Page(page)
			return result()
		}

		if SameSite(currentHost, tu.Hostname(), cfg.IncludeSubdomains) {
			s.Page(page)
			currentURL = target
			continue
		}

		// Cross-host redirect at depth 0.
		if hops >= maxSeedHostHops {
			s.Page(page)
			return result()
		}
		s.Page(page)
		hops++
		currentURL = target
		currentHost = tu.Hostname()
		currentBase = tu
		currentRobots = resolveRobots(ctx, cfg, f, tu, stats)

		newDelay := cfg.Delay
		if rd := currentRobots.CrawlDelay(cfg.RobotsToken); rd > newDelay {
			newDelay = rd
		}
		limiter.delay = newDelay
	}
}

// resolveRobots fetches and parses robots.txt for the seed's host, per the
// rules in specs/002-crawler.md: 2xx parses normally, 4xx allows
// everything, 5xx or a transport error disallows everything, and
// IgnoreRobots skips the request entirely.
func resolveRobots(ctx context.Context, cfg Config, f Fetcher, su *url.URL, stats *Stats) *Robots {
	if cfg.IgnoreRobots {
		return AllowAll()
	}

	robotsURL := su.Scheme + "://" + su.Host + "/robots.txt"
	resp, err := f.Fetch(ctx, robotsURL)
	if err != nil || resp == nil {
		if err == nil {
			err = errors.New("respuesta vacía")
		}
		stats.RobotsError = "robots.txt: " + err.Error()
		return DisallowAllBecause(stats.RobotsError)
	}
	stats.RobotsTxt = string(resp.Body)
	stats.RobotsError = ""

	switch {
	case resp.Status >= 200 && resp.Status < 300:
		return ParseRobots(bytes.NewReader(resp.Body))
	case resp.Status >= 500:
		stats.RobotsError = fmt.Sprintf("robots.txt: HTTP %d", resp.Status)
		return DisallowAllBecause(stats.RobotsError)
	default:
		return AllowAll()
	}
}

// engineState is the mutable state shared by every worker goroutine.
type engineState struct {
	mu       sync.Mutex
	fr       *Frontier
	fetched  int
	blocked  int
	errs     int
	inFlight int
}

// runWorker pops URLs off the shared frontier until it is empty, MaxPages
// worth of fetches have been committed (fetched + in-flight), or ctx is
// cancelled.
func runWorker(ctx context.Context, cfg Config, f Fetcher, s Sink, robots *Robots, seedHost string, st *engineState, limiter *rateLimiter) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		st.mu.Lock()
		if st.fetched+st.inFlight >= cfg.MaxPages {
			st.mu.Unlock()
			return
		}
		u, depth, ok := st.fr.Pop()
		if !ok {
			st.mu.Unlock()
			return
		}

		if !robots.Allowed(cfg.RobotsToken, robotsPath(u)) {
			st.blocked++
			st.mu.Unlock()
			s.Page(Page{URL: u, Depth: depth, Blocked: true, Error: robots.BlockReason(), FetchedAt: time.Now()})
			continue
		}

		st.inFlight++
		st.mu.Unlock()

		page := fetchAndBuildPage(ctx, cfg, f, seedHost, u, depth, limiter)

		st.mu.Lock()
		st.inFlight--
		st.fetched++
		if page.Error != "" {
			st.errs++
		}
		if !page.Blocked {
			enqueueLinks(page, cfg, seedHost, st.fr)
		}
		st.mu.Unlock()

		s.Page(page)
	}
}

func robotsPath(rawURL string) string {
	pu, err := url.Parse(rawURL)
	if err != nil {
		return "/"
	}
	return pu.RequestURI()
}

// fetchAndBuildPage fetches u and turns the Fetcher's Response into a Page,
// extracting HTML metadata and links when appropriate.
func fetchAndBuildPage(ctx context.Context, cfg Config, f Fetcher, seedHost string, u string, depth int, limiter *rateLimiter) Page {
	limiter.wait(ctx)

	page := Page{URL: u, Depth: depth, FetchedAt: time.Now()}

	resp, err := f.Fetch(ctx, u)
	if err != nil {
		page.Error = err.Error()
		return page
	}

	page.Status = resp.Status
	page.Size = resp.Size
	page.Duration = resp.Duration
	page.ContentType = mediaType(resp.ContentType)

	if resp.Status >= 300 && resp.Status < 400 {
		if resp.Location != "" {
			if pu, err := url.Parse(u); err == nil {
				if norm, err := Normalize(resp.Location, pu); err == nil {
					page.RedirectTo = norm
				}
			}
		}
		return page
	}

	if page.ContentType == "text/html" || page.ContentType == "application/xhtml+xml" {
		pu, err := url.Parse(u)
		if err == nil {
			if extracted, err := ParseHTML(resp.Body, pu, seedHost, cfg.IncludeSubdomains); err == nil {
				page.Title = extracted.Title
				page.Description = extracted.Description
				page.Canonical = extracted.Canonical
				page.MetaRobots = extracted.MetaRobots
				page.NoIndex = extracted.NoIndex
				page.NoFollow = extracted.NoFollow
				page.H1 = extracted.H1
				page.Links = extracted.Links
			}
		}
	}

	return page
}

// mediaType extracts the lower-case media type (without parameters) from a
// raw Content-Type header value.
func mediaType(contentType string) string {
	if contentType == "" {
		return ""
	}
	if mt, _, err := mime.ParseMediaType(contentType); err == nil {
		return strings.ToLower(mt)
	}
	ct := contentType
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = ct[:idx]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

// enqueueLinks pushes a fetched page's outbound links and redirect target
// into the frontier, honouring scope, nofollow and MaxDepth.
func enqueueLinks(page Page, cfg Config, seedHost string, fr *Frontier) {
	if page.Error != "" {
		return
	}

	if !page.NoFollow && page.Depth+1 <= cfg.MaxDepth {
		for _, l := range page.Links {
			if l.InScope && !l.NoFollow {
				fr.Push(l.URL, page.Depth+1)
			}
		}
	}

	if page.RedirectTo != "" {
		if pu, err := url.Parse(page.RedirectTo); err == nil {
			if SameSite(seedHost, pu.Hostname(), cfg.IncludeSubdomains) {
				fr.Push(page.RedirectTo, page.Depth)
			}
		}
	}
}

// rateLimiter ensures at least delay elapses between the start of two
// requests, across every worker goroutine.
type rateLimiter struct {
	mu    sync.Mutex
	delay time.Duration
	next  time.Time
}

func (r *rateLimiter) wait(ctx context.Context) {
	if r.delay <= 0 {
		return
	}

	r.mu.Lock()
	now := time.Now()
	var wait time.Duration
	if now.Before(r.next) {
		wait = r.next.Sub(now)
		r.next = r.next.Add(r.delay)
	} else {
		r.next = now.Add(r.delay)
	}
	r.mu.Unlock()

	if wait <= 0 {
		return
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}
