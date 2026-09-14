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
	Sitemaps                 int // URLs discovered via sitemaps
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
	fr.Push(normalizedSeed, 0)

	var stats Stats
	robots := resolveRobots(ctx, cfg, f, su, &stats)

	delay := cfg.Delay
	if rd := robots.CrawlDelay(cfg.RobotsToken); rd > delay {
		delay = rd
	}

	collectSitemaps(ctx, cfg, f, robots, su, seedHost, fr, &stats)

	state := &engineState{fr: fr}
	limiter := &rateLimiter{delay: delay}

	var wg sync.WaitGroup
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runWorker(ctx, cfg, f, s, robots, seedHost, state, limiter)
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
		return DisallowAll()
	}
	stats.RobotsTxt = string(resp.Body)

	switch {
	case resp.Status >= 200 && resp.Status < 300:
		return ParseRobots(bytes.NewReader(resp.Body))
	case resp.Status >= 500:
		return DisallowAll()
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
			s.Page(Page{URL: u, Depth: depth, Blocked: true, FetchedAt: time.Now()})
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
