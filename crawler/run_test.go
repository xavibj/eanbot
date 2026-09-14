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

// Note: a depth-0 (seed) redirect to a different host used to stop the
// crawl right there; specs/002-crawler.md now requires the crawler to
// follow it and adopt the new host as scope instead. See
// TestRunSeedRedirectChangesScope for that behavior, and
// TestRunDepth1CrossHostRedirectStillNotFollowed for confirmation that
// deeper (depth > 0) cross-host redirects are still not followed.

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

func TestRunSeedRedirectChangesScope(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://old.example/", redirectResponse(301, "https://new.example/"))
	f.set("https://new.example/", htmlResponse(`<a href="/page2">p2</a>`))
	f.set("https://new.example/page2", htmlResponse(``))

	sink := &memorySink{}
	cfg := Config{Seed: "https://old.example/", Concurrency: 1, IgnoreRobots: true, MaxPages: 10, MaxDepth: 10}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.FinalHost != "new.example" {
		t.Errorf("FinalHost = %q, want new.example", stats.FinalHost)
	}
	if stats.Fetched != 3 {
		t.Errorf("Fetched = %d, want 3", stats.Fetched)
	}
	want := []string{
		"https://old.example/",
		"https://new.example/",
		"https://new.example/page2",
	}
	got := sink.urls()
	if len(got) != len(want) {
		t.Fatalf("sink urls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sink order[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	pages := sink.all()
	if pages[0].RedirectTo != "https://new.example/" {
		t.Errorf("seed RedirectTo = %q, want https://new.example/", pages[0].RedirectTo)
	}
}

func TestRunSeedRedirectChainTwoHops(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://a.example/", redirectResponse(302, "https://b.example/"))
	f.set("https://b.example/", redirectResponse(302, "https://c.example/"))
	f.set("https://c.example/", htmlResponse(``))

	sink := &memorySink{}
	cfg := Config{Seed: "https://a.example/", Concurrency: 1, IgnoreRobots: true, MaxPages: 10, MaxDepth: 10}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.FinalHost != "c.example" {
		t.Errorf("FinalHost = %q, want c.example", stats.FinalHost)
	}
	if stats.Fetched != 3 {
		t.Errorf("Fetched = %d, want 3", stats.Fetched)
	}
	want := []string{"https://a.example/", "https://b.example/", "https://c.example/"}
	got := sink.urls()
	if len(got) != len(want) {
		t.Fatalf("sink urls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sink order[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRunSeedRedirectHopLimit(t *testing.T) {
	const hosts = 7 // host0..host6
	f := newFakeFetcher()
	for i := 0; i < hosts-1; i++ {
		from := fmt.Sprintf("https://host%d.example/", i)
		to := fmt.Sprintf("https://host%d.example/", i+1)
		f.set(from, redirectResponse(302, to))
	}
	// host6 must never be fetched: no response configured for it, so the
	// fake fetcher would return an error if the crawler mistakenly tried.

	sink := &memorySink{}
	cfg := Config{Seed: "https://host0.example/", Concurrency: 1, IgnoreRobots: true, MaxPages: 100, MaxDepth: 10}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	// 5 host switches are allowed: host0->1->2->3->4->5 (5 switches),
	// landing on host5. host5's own redirect to host6 is the 6th switch
	// attempt, which must be refused.
	if stats.FinalHost != "host5.example" {
		t.Errorf("FinalHost = %q, want host5.example", stats.FinalHost)
	}
	if stats.Fetched != 6 {
		t.Errorf("Fetched = %d, want 6", stats.Fetched)
	}
	for _, u := range f.calledURLs() {
		if u == "https://host6.example/" {
			t.Fatal("host6 must never be fetched (hop limit exceeded)")
		}
	}
	pages := sink.all()
	if len(pages) != 6 {
		t.Fatalf("sink got %d pages, want 6", len(pages))
	}
	last := pages[len(pages)-1]
	if last.URL != "https://host5.example/" || last.RedirectTo != "https://host6.example/" {
		t.Errorf("last page = %+v, want URL host5.example with RedirectTo host6.example", last)
	}
}

func TestRunDepth1CrossHostRedirectStillNotFollowed(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://a.example/", htmlResponse(`<a href="/out">out</a>`))
	f.set("https://a.example/out", redirectResponse(302, "https://other.example/"))

	sink := &memorySink{}
	cfg := Config{Seed: "https://a.example/", Concurrency: 1, IgnoreRobots: true, MaxPages: 10, MaxDepth: 10}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.FinalHost != "a.example" {
		t.Errorf("FinalHost = %q, want a.example (depth-1 redirect must not change scope)", stats.FinalHost)
	}
	if stats.Fetched != 2 {
		t.Errorf("Fetched = %d, want 2", stats.Fetched)
	}
	if stats.Queued != 0 {
		t.Errorf("Queued = %d, want 0", stats.Queued)
	}
	for _, u := range f.calledURLs() {
		if u == "https://other.example/" {
			t.Fatal("other.example must never be fetched (depth > 0 cross-host redirect)")
		}
	}
}

func TestRunSeedRedirectAppliesNewHostRobots(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://old.example/robots.txt", robotsResponse(""))
	f.set("https://old.example/", redirectResponse(301, "https://new.example/"))
	newRobotsBody := "User-agent: eanbot\nDisallow: /private/\n\nUser-agent: *\nDisallow:\n"
	f.set("https://new.example/robots.txt", robotsResponse(newRobotsBody))
	f.set("https://new.example/", htmlResponse(`<a href="/private/x">x</a><a href="/public/y">y</a>`))
	f.set("https://new.example/public/y", htmlResponse(``))

	sink := &memorySink{}
	cfg := Config{Seed: "https://old.example/", Concurrency: 1, MaxPages: 10, MaxDepth: 10, UseSitemaps: false}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.FinalHost != "new.example" {
		t.Errorf("FinalHost = %q, want new.example", stats.FinalHost)
	}
	if stats.RobotsTxt != newRobotsBody {
		t.Errorf("RobotsTxt = %q, want the new host's robots.txt body", stats.RobotsTxt)
	}
	if stats.Fetched != 3 {
		t.Errorf("Fetched = %d, want 3", stats.Fetched)
	}
	if stats.Blocked != 1 {
		t.Errorf("Blocked = %d, want 1", stats.Blocked)
	}
	var sawBlocked bool
	for _, p := range sink.all() {
		if p.URL == "https://new.example/private/x" {
			sawBlocked = true
			if !p.Blocked {
				t.Error("/private/x should be Blocked under the new host's robots.txt")
			}
		}
	}
	if !sawBlocked {
		t.Error("expected a page for https://new.example/private/x")
	}
}

func TestRunSitemapsUseFinalHostAfterSeedRedirect(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://old.example/robots.txt", robotsResponse("")) // no sitemap here
	f.set("https://old.example/", redirectResponse(301, "https://new.example/"))
	f.set("https://new.example/robots.txt", robotsResponse("Sitemap: https://new.example/sitemap.xml\n"))
	f.set("https://new.example/sitemap.xml", &Response{
		Status:      200,
		ContentType: "application/xml",
		Body:        []byte(`<urlset><url><loc>https://new.example/from-sitemap</loc></url></urlset>`),
	})
	f.set("https://new.example/", htmlResponse(``))
	f.set("https://new.example/from-sitemap", htmlResponse(``))

	sink := &memorySink{}
	cfg := Config{Seed: "https://old.example/", Concurrency: 1, MaxPages: 10, MaxDepth: 10, UseSitemaps: true}

	stats, err := Run(context.Background(), cfg, f, sink)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if stats.FinalHost != "new.example" {
		t.Errorf("FinalHost = %q, want new.example", stats.FinalHost)
	}
	if stats.Sitemaps != 1 {
		t.Errorf("Sitemaps = %d, want 1", stats.Sitemaps)
	}
	var sawSitemapPage bool
	for _, p := range sink.all() {
		if p.URL == "https://new.example/from-sitemap" {
			sawSitemapPage = true
		}
	}
	if !sawSitemapPage {
		t.Error("expected the sitemap-discovered URL (from the new host) to be crawled")
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
