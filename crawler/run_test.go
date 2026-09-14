package crawler

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(s)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func TestRunBFSDeterministic(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://example.com/", htmlResponse(`<a href="/a">a</a><a href="/b">b</a>`))
	f.set("https://example.com/a", htmlResponse(`<a href="/c">c</a>`))
	f.set("https://example.com/b", htmlResponse(`<a href="/d">d</a>`))
	f.set("https://example.com/c", htmlResponse(``))
	f.set("https://example.com/d", htmlResponse(``))

	sink := &memorySink{}
	cfg := Config{Seed: "https://example.com/", Concurrency: 1, IgnoreRobots: true, MaxPages: 10, MaxDepth: 10}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	want := []string{
		"https://example.com/",
		"https://example.com/a",
		"https://example.com/b",
		"https://example.com/c",
		"https://example.com/d",
	}
	got := sink.urls()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sink order[%d] = %q, want %q (got %v)", i, got[i], want[i], got)
		}
	}

	if stats.Fetched != 5 || stats.Blocked != 0 || stats.Errors != 0 || stats.Queued != 0 {
		t.Errorf("stats = %+v, want Fetched=5 Blocked=0 Errors=0 Queued=0", stats)
	}
}

func TestRunMaxPages(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://example.com/", htmlResponse(`<a href="/a">a</a><a href="/b">b</a><a href="/c">c</a>`))
	f.set("https://example.com/a", htmlResponse(``))

	sink := &memorySink{}
	cfg := Config{Seed: "https://example.com/", Concurrency: 1, IgnoreRobots: true, MaxPages: 2, MaxDepth: 10}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	if stats.Fetched != 2 {
		t.Errorf("Fetched = %d, want 2", stats.Fetched)
	}
	if stats.Queued != 2 {
		t.Errorf("Queued = %d, want 2", stats.Queued)
	}
	if got := sink.urls(); len(got) != 2 {
		t.Errorf("sink pages = %v, want 2 entries", got)
	}
}

func TestRunMaxDepth(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://example.com/", htmlResponse(`<a href="/a">a</a>`))
	f.set("https://example.com/a", htmlResponse(`<a href="/b">b</a>`))

	sink := &memorySink{}
	cfg := Config{Seed: "https://example.com/", Concurrency: 1, IgnoreRobots: true, MaxPages: 100, MaxDepth: 1}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	if stats.Fetched != 2 {
		t.Errorf("Fetched = %d, want 2", stats.Fetched)
	}
	if stats.Queued != 0 {
		t.Errorf("Queued = %d, want 0", stats.Queued)
	}
	if f.callCount() != 2 {
		t.Errorf("fetch calls = %d, want 2 (b must never be fetched)", f.callCount())
	}
}

func TestRunRobotsAllowOn4xx(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://example.com/robots.txt", textResponse(404, "text/plain", "not found"))
	f.set("https://example.com/", htmlResponse(``))

	sink := &memorySink{}
	cfg := Config{Seed: "https://example.com/", Concurrency: 1, MaxPages: 10, MaxDepth: 10, UseSitemaps: false}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.Blocked != 0 || stats.Fetched != 1 {
		t.Errorf("stats = %+v, want Blocked=0 Fetched=1", stats)
	}
	if stats.RobotsTxt != "not found" {
		t.Errorf("RobotsTxt = %q", stats.RobotsTxt)
	}
}

func TestRunRobotsDisallowOn5xx(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://example.com/robots.txt", textResponse(500, "text/plain", "boom"))

	sink := &memorySink{}
	cfg := Config{Seed: "https://example.com/", Concurrency: 1, MaxPages: 10, MaxDepth: 10, UseSitemaps: false}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.Blocked != 1 || stats.Fetched != 0 {
		t.Errorf("stats = %+v, want Blocked=1 Fetched=0", stats)
	}
	pages := sink.all()
	if len(pages) != 1 || !pages[0].Blocked || pages[0].Status != 0 {
		t.Errorf("pages = %+v, want a single Blocked page with Status 0", pages)
	}
}

func TestRunRobotsSpecificGroup(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://example.com/robots.txt", robotsResponse(`
User-agent: eanbot
Disallow: /private/

User-agent: *
Disallow:
`))
	f.set("https://example.com/", htmlResponse(`<a href="/private/x">x</a><a href="/public/y">y</a>`))
	f.set("https://example.com/public/y", htmlResponse(``))

	sink := &memorySink{}
	cfg := Config{Seed: "https://example.com/", Concurrency: 1, MaxPages: 10, MaxDepth: 10, UseSitemaps: false}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.Fetched != 2 {
		t.Errorf("Fetched = %d, want 2", stats.Fetched)
	}
	if stats.Blocked != 1 {
		t.Errorf("Blocked = %d, want 1", stats.Blocked)
	}

	var sawBlocked bool
	for _, p := range sink.all() {
		if p.URL == "https://example.com/private/x" {
			sawBlocked = true
			if !p.Blocked {
				t.Error("/private/x should be Blocked")
			}
		}
	}
	if !sawBlocked {
		t.Error("expected a page for /private/x")
	}
}

func TestRunRedirectInScope(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://example.com/", redirectResponse(301, "/moved"))
	f.set("https://example.com/moved", htmlResponse(``))

	sink := &memorySink{}
	cfg := Config{Seed: "https://example.com/", Concurrency: 1, IgnoreRobots: true, MaxPages: 10, MaxDepth: 10}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.Fetched != 2 {
		t.Errorf("Fetched = %d, want 2", stats.Fetched)
	}
	pages := sink.all()
	if len(pages) != 2 {
		t.Fatalf("pages = %+v, want 2", pages)
	}
	if pages[0].RedirectTo != "https://example.com/moved" {
		t.Errorf("RedirectTo = %q, want https://example.com/moved", pages[0].RedirectTo)
	}
	if pages[1].URL != "https://example.com/moved" {
		t.Errorf("second page URL = %q", pages[1].URL)
	}
}

func TestRunRedirectOutOfScope(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://example.com/", redirectResponse(301, "https://other.com/x"))

	sink := &memorySink{}
	cfg := Config{Seed: "https://example.com/", Concurrency: 1, IgnoreRobots: true, MaxPages: 10, MaxDepth: 10}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.Fetched != 1 {
		t.Errorf("Fetched = %d, want 1", stats.Fetched)
	}
	if stats.Queued != 0 {
		t.Errorf("Queued = %d, want 0", stats.Queued)
	}
	if f.callCount() != 1 {
		t.Errorf("fetch calls = %d, want 1 (other.com must never be fetched)", f.callCount())
	}
	pages := sink.all()
	if len(pages) != 1 || pages[0].RedirectTo != "https://other.com/x" {
		t.Errorf("pages = %+v", pages)
	}
}

func TestRunMetaNoFollowStopsAllLinks(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://example.com/", htmlResponse(`<meta name="robots" content="nofollow"><a href="/a">a</a>`))

	sink := &memorySink{}
	cfg := Config{Seed: "https://example.com/", Concurrency: 1, IgnoreRobots: true, MaxPages: 10, MaxDepth: 10}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.Fetched != 1 {
		t.Errorf("Fetched = %d, want 1", stats.Fetched)
	}
	if f.callCount() != 1 {
		t.Errorf("fetch calls = %d, want 1 (/a must never be fetched)", f.callCount())
	}
}

func TestRunAnchorNoFollowSkipsThatLinkOnly(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://example.com/", htmlResponse(`<a href="/a" rel="nofollow">a</a><a href="/b">b</a>`))
	f.set("https://example.com/b", htmlResponse(``))

	sink := &memorySink{}
	cfg := Config{Seed: "https://example.com/", Concurrency: 1, IgnoreRobots: true, MaxPages: 10, MaxDepth: 10}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.Fetched != 2 {
		t.Errorf("Fetched = %d, want 2", stats.Fetched)
	}
	for _, u := range f.calledURLs() {
		if u == "https://example.com/a" {
			t.Error("/a should never be fetched (anchor rel=nofollow)")
		}
	}
}

func TestRunSitemapIndexAndGzip(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://example.com/robots.txt", robotsResponse("Sitemap: https://example.com/sitemap-index.xml\n"))
	f.set("https://example.com/sitemap-index.xml", &Response{
		Status:      200,
		ContentType: "application/xml",
		Body:        []byte(`<sitemapindex><sitemap><loc>https://example.com/sitemap-1.xml.gz</loc></sitemap></sitemapindex>`),
	})
	f.set("https://example.com/sitemap-1.xml.gz", &Response{
		Status:      200,
		ContentType: "application/gzip",
		Body:        gzipBytes(t, `<urlset><url><loc>https://example.com/from-sitemap</loc></url></urlset>`),
	})
	f.set("https://example.com/", htmlResponse(``))
	f.set("https://example.com/from-sitemap", htmlResponse(``))

	sink := &memorySink{}
	cfg := Config{Seed: "https://example.com/", Concurrency: 1, MaxPages: 10, MaxDepth: 10, UseSitemaps: true}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.Sitemaps != 1 {
		t.Errorf("Sitemaps = %d, want 1", stats.Sitemaps)
	}
	if stats.Fetched != 2 {
		t.Errorf("Fetched = %d, want 2", stats.Fetched)
	}
	var sawSitemapPage bool
	for _, p := range sink.all() {
		if p.URL == "https://example.com/from-sitemap" {
			sawSitemapPage = true
		}
	}
	if !sawSitemapPage {
		t.Error("expected the sitemap-discovered URL to be crawled")
	}
}

func TestRunCancellation(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://example.com/", htmlResponse(`<a href="/a">a</a><a href="/b">b</a><a href="/c">c</a>`))

	ctx, cancel := context.WithCancel(context.Background())
	f.onCall = func(url string, idx int) {
		if idx == 1 {
			cancel()
		}
	}

	sink := &memorySink{}
	cfg := Config{Seed: "https://example.com/", Concurrency: 1, IgnoreRobots: true, MaxPages: 100, MaxDepth: 10}

	stats, err := Run(ctx, cfg, f, sink)
	if err == nil {
		t.Fatal("expected an error wrapping context.Canceled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap context.Canceled", err)
	}
	if stats.Fetched != 2 {
		t.Errorf("Fetched = %d, want 2 (seed + the cancelled fetch)", stats.Fetched)
	}
	if stats.Errors != 1 {
		t.Errorf("Errors = %d, want 1", stats.Errors)
	}
	if stats.Queued != 2 {
		t.Errorf("Queued = %d, want 2 (b and c left unvisited)", stats.Queued)
	}
}

func TestRunNonHTMLHasNoLinks(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://example.com/", textResponse(200, "application/pdf", `<a href="/should-not-be-found">ignored</a>`))

	sink := &memorySink{}
	cfg := Config{Seed: "https://example.com/", Concurrency: 1, IgnoreRobots: true, MaxPages: 10, MaxDepth: 10}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.Fetched != 1 {
		t.Errorf("Fetched = %d, want 1", stats.Fetched)
	}
	if f.callCount() != 1 {
		t.Errorf("fetch calls = %d, want 1", f.callCount())
	}
	pages := sink.all()
	if len(pages) != 1 {
		t.Fatalf("pages = %+v", pages)
	}
	if pages[0].ContentType != "application/pdf" {
		t.Errorf("ContentType = %q, want application/pdf", pages[0].ContentType)
	}
	if len(pages[0].Links) != 0 {
		t.Errorf("Links = %+v, want none", pages[0].Links)
	}
}

func TestRunConcurrencyRespectsMaxPagesExactly(t *testing.T) {
	const children = 19
	f := newFakeFetcher()
	var seedBody bytes.Buffer
	universe := map[string]bool{"https://example.com/": true}
	for i := 0; i < children; i++ {
		u := fmt.Sprintf("https://example.com/c%d", i)
		universe[u] = true
		fmt.Fprintf(&seedBody, `<a href="/c%d">c%d</a>`, i, i)
		f.set(u, htmlResponse(``))
	}
	f.set("https://example.com/", htmlResponse(seedBody.String()))

	sink := &memorySink{}
	cfg := Config{Seed: "https://example.com/", Concurrency: 4, IgnoreRobots: true, MaxPages: 5, MaxDepth: 10}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.Fetched != 5 {
		t.Fatalf("Fetched = %d, want exactly 5", stats.Fetched)
	}
	if stats.Queued != children+1-5 {
		t.Errorf("Queued = %d, want %d", stats.Queued, children+1-5)
	}

	pages := sink.all()
	if len(pages) != 5 {
		t.Fatalf("sink got %d pages, want 5", len(pages))
	}
	seen := map[string]bool{}
	for _, p := range pages {
		if !universe[p.URL] {
			t.Errorf("unexpected URL %q", p.URL)
		}
		if seen[p.URL] {
			t.Errorf("duplicate URL %q delivered to sink", p.URL)
		}
		seen[p.URL] = true
	}
}

// smoke test to make sure Run surfaces Validate() errors joined by "; ".
func TestRunValidatesConfig(t *testing.T) {
	f := newFakeFetcher()
	sink := &memorySink{}
	_, err := Run(context.Background(), Config{}, f, sink)
	if err == nil {
		t.Fatal("expected a validation error")
	}
}

func TestRunTimeoutIsBoundedInPractice(t *testing.T) {
	// Guard against accidental deadlocks in the worker pool: this must
	// complete quickly.
	done := make(chan struct{})
	go func() {
		f := newFakeFetcher()
		f.set("https://example.com/", htmlResponse(``))
		sink := &memorySink{}
		cfg := Config{Seed: "https://example.com/", Concurrency: 4, IgnoreRobots: true, MaxPages: 10, MaxDepth: 10}
		Run(context.Background(), cfg, f, sink)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not finish in time")
	}
}
