package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"xavi.net/eanbot/crawler"
	"xavi.net/eanbot/store"
)

// ValidationError is returned by manager.Start when the requested
// configuration fails crawler.Config.Validate(); it carries every
// violation found, so the API can report them all at once.
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	return strings.Join(e.Errors, "; ")
}

// manager runs crawls in the background: each call to Start launches a
// goroutine that drives crawler.Run to completion (or cancellation) and
// persists its pages and final status via store.
type manager struct {
	st         *store.Store
	newFetcher func(cfg crawler.Config) crawler.Fetcher
	log        *log.Logger

	mu      sync.Mutex
	running map[int64]context.CancelFunc
	wg      sync.WaitGroup
}

func newManager(st *store.Store, newFetcher func(crawler.Config) crawler.Fetcher, logger *log.Logger) *manager {
	return &manager{
		st:         st,
		newFetcher: newFetcher,
		log:        logger,
		running:    make(map[int64]context.CancelFunc),
	}
}

// Start validates cfg (after applying defaults), creates the crawl record
// and launches the goroutine that runs it. It returns the new crawl's id.
func (m *manager) Start(cfg crawler.Config) (int64, error) {
	cfg = cfg.WithDefaults()
	if errs := cfg.Validate(); len(errs) > 0 {
		return 0, &ValidationError{Errors: errs}
	}

	configJSON, err := configToJSON(cfg)
	if err != nil {
		return 0, fmt.Errorf("server: encode config: %w", err)
	}

	crawl, err := m.st.CreateCrawl(cfg.Seed, configJSON)
	if err != nil {
		return 0, fmt.Errorf("server: create crawl: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.running[crawl.ID] = cancel
	m.mu.Unlock()

	fetcher := m.newFetcher(cfg)
	// batchMaxItems/batchMaxDelay match specs/004-api-web.md's Manager
	// contract (100 pages or 1s, whichever comes first): with a crawl of
	// over a million pages, a commit per page grew the WAL far faster than
	// checkpoints could reclaim it (see "Lotes" in specs/003-store.md).
	sink := &storeSink{
		st:      m.st,
		crawlID: crawl.ID,
		log:     m.log,
		writer:  store.NewBatchWriter(m.st, crawl.ID, 100, time.Second),
	}

	m.wg.Add(1)
	go m.run(ctx, cancel, crawl.ID, cfg, fetcher, sink)

	return crawl.ID, nil
}

func (m *manager) run(ctx context.Context, cancel context.CancelFunc, crawlID int64, cfg crawler.Config, fetcher crawler.Fetcher, sink *storeSink) {
	defer m.wg.Done()
	defer cancel()

	m.log.Printf("crawl %d: started (seed=%s)", crawlID, cfg.Seed)

	stats, err := crawler.Run(ctx, cfg, fetcher, sink)

	// Close before FinishCrawl: it flushes whatever is still buffered, so
	// the crawl is never marked done/failed/cancelled with pages still
	// missing from the store (this matters most for crawls that finish in
	// well under BatchWriter's 1s maxDelay).
	if werr := sink.writer.Close(); werr != nil {
		m.log.Printf("crawl %d: flush pages: %v", crawlID, werr)
	}

	// The engine's via_nofollow mark is an overapproximation under
	// concurrency (see specs/002-crawler.md): fix it up now that every page
	// and link of the crawl has landed, before the crawl is reported done.
	if cfg.FollowNoFollow {
		if cleared, rerr := m.st.RecomputeViaNoFollow(crawlID); rerr != nil {
			m.log.Printf("crawl %d: recompute via_nofollow: %v", crawlID, rerr)
		} else {
			m.log.Printf("crawl %d: via_nofollow recalculado (%d limpiadas)", crawlID, cleared)
		}
	}

	if serr := m.st.SetRobots(crawlID, stats.RobotsTxt); serr != nil {
		m.log.Printf("crawl %d: set robots: %v", crawlID, serr)
	}

	status := "done"
	errMsg := ""
	switch {
	case errors.Is(err, context.Canceled):
		status = "cancelled"
	case err != nil:
		status = "failed"
		errMsg = err.Error()
	}

	if ferr := m.st.FinishCrawl(crawlID, status, errMsg); ferr != nil {
		m.log.Printf("crawl %d: finish crawl: %v", crawlID, ferr)
	}
	m.log.Printf("crawl %d: finished (status=%s)", crawlID, status)

	m.mu.Lock()
	delete(m.running, crawlID)
	m.mu.Unlock()
}

// Cancel requests cancellation of a running crawl. It returns false if the
// crawl was not running (already finished, or unknown id) — that is not an
// error, per specs/004-api-web.md.
func (m *manager) Cancel(id int64) bool {
	m.mu.Lock()
	cancel, ok := m.running[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// Running returns the ids of every crawl currently in flight.
func (m *manager) Running() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]int64, 0, len(m.running))
	for id := range m.running {
		ids = append(ids, id)
	}
	return ids
}

// isRunning reports whether the given crawl is currently in flight.
func (m *manager) isRunning(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.running[id]
	return ok
}

// shutdown cancels every running crawl and waits for their goroutines to
// finish, or for ctx to be done, whichever happens first.
func (m *manager) shutdown(ctx context.Context) error {
	m.mu.Lock()
	for _, cancel := range m.running {
		cancel()
	}
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// --- crawler.Sink implementation: persists every page via store ---

// storeSink adapts crawler.Sink to store.BatchWriter, converting types as
// it goes. Page queues into writer rather than writing synchronously (see
// "Lotes" in specs/003-store.md); a page that ultimately fails to persist
// (e.g. a duplicate URL, which should not happen given the frontier's own
// deduplication) is not reported per-page -- the batch's error is logged
// once, in manager.run, after writer.Close().
type storeSink struct {
	st      *store.Store
	crawlID int64
	log     *log.Logger
	writer  *store.BatchWriter
}

func (sk *storeSink) Page(p crawler.Page) {
	sp := store.Page{
		CrawlID:     sk.crawlID,
		URL:         p.URL,
		Depth:       p.Depth,
		Status:      p.Status,
		ContentType: p.ContentType,
		Size:        p.Size,
		DurationMs:  p.Duration.Milliseconds(),
		Title:       p.Title,
		Description: p.Description,
		Canonical:   p.Canonical,
		MetaRobots:  p.MetaRobots,
		XRobotsTag:  p.XRobotsTag,
		ViaNoFollow: p.ViaNoFollow,
		NoIndex:     p.NoIndex,
		NoFollow:    p.NoFollow,
		H1:          p.H1,
		RedirectTo:  p.RedirectTo,
		Error:       p.Error,
		Blocked:     p.Blocked,
		FetchedAt:   p.FetchedAt,
	}

	links := make([]store.Link, 0, len(p.Links))
	for _, l := range p.Links {
		links = append(links, store.Link{
			ToURL:    l.URL,
			Text:     l.Text,
			NoFollow: l.NoFollow,
			InScope:  l.InScope,
		})
	}

	sk.writer.Add(sp, links)
}

// --- config <-> JSON ---

// configDoc is the JSON shape of the "config" object fixed by
// specs/004-api-web.md, both in POST /api/crawls requests and as stored on
// the crawl record.
type configDoc struct {
	Seed              string            `json:"seed"`
	MaxPages          int               `json:"max_pages"`
	MaxDepth          int               `json:"max_depth"`
	Concurrency       int               `json:"concurrency"`
	DelayMs           *int              `json:"delay_ms"`
	TimeoutMs         int               `json:"timeout_ms"`
	MaxBodyBytes      int64             `json:"max_body_bytes"`
	UserAgent         string            `json:"user_agent"`
	IncludeSubdomains bool              `json:"include_subdomains"`
	IgnoreRobots      bool              `json:"ignore_robots"`
	UseSitemaps       *bool             `json:"use_sitemaps"`
	Headers           map[string]string `json:"headers"`
	Origin            string            `json:"origin"`
	InsecureTLS       bool              `json:"insecure_tls"`
	FollowNoFollow    bool              `json:"follow_nofollow"`
}

// configToCrawlerConfig converts a decoded request body into a
// crawler.Config, pre-defaults. delay_ms and use_sitemaps get their
// documented default (500ms, true) when absent from the request, since
// crawler.Config.WithDefaults deliberately leaves both zero-valued Delay
// and every bool field untouched (see crawler/config.go).
func configToCrawlerConfig(doc configDoc) crawler.Config {
	cfg := crawler.Config{
		Seed:              doc.Seed,
		MaxPages:          doc.MaxPages,
		MaxDepth:          doc.MaxDepth,
		Concurrency:       doc.Concurrency,
		Timeout:           time.Duration(doc.TimeoutMs) * time.Millisecond,
		MaxBodyBytes:      doc.MaxBodyBytes,
		UserAgent:         doc.UserAgent,
		IncludeSubdomains: doc.IncludeSubdomains,
		IgnoreRobots:      doc.IgnoreRobots,
		Headers:           doc.Headers,
		Origin:            doc.Origin,
		InsecureTLS:       doc.InsecureTLS,
		FollowNoFollow:    doc.FollowNoFollow,
	}

	if doc.DelayMs != nil {
		cfg.Delay = time.Duration(*doc.DelayMs) * time.Millisecond
	} else {
		cfg.Delay = 500 * time.Millisecond
	}

	if doc.UseSitemaps != nil {
		cfg.UseSitemaps = *doc.UseSitemaps
	} else {
		cfg.UseSitemaps = true
	}

	return cfg
}

// configToJSON serializes cfg (expected to already have WithDefaults
// applied) as the canonical "config" object.
func configToJSON(cfg crawler.Config) (json.RawMessage, error) {
	delayMs := int(cfg.Delay.Milliseconds())
	useSitemaps := cfg.UseSitemaps
	headers := cfg.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	doc := configDoc{
		Seed:              cfg.Seed,
		MaxPages:          cfg.MaxPages,
		MaxDepth:          cfg.MaxDepth,
		Concurrency:       cfg.Concurrency,
		DelayMs:           &delayMs,
		TimeoutMs:         int(cfg.Timeout.Milliseconds()),
		MaxBodyBytes:      cfg.MaxBodyBytes,
		UserAgent:         cfg.UserAgent,
		IncludeSubdomains: cfg.IncludeSubdomains,
		IgnoreRobots:      cfg.IgnoreRobots,
		UseSitemaps:       &useSitemaps,
		Headers:           headers,
		Origin:            cfg.Origin,
		InsecureTLS:       cfg.InsecureTLS,
		FollowNoFollow:    cfg.FollowNoFollow,
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return b, nil
}
