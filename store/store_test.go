package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "eanbot.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return s
}

func mustCreateCrawl(t *testing.T, s *Store, seed string) *Crawl {
	t.Helper()
	c, err := s.CreateCrawl(seed, json.RawMessage(`{"seed":"`+seed+`"}`))
	if err != nil {
		t.Fatalf("CreateCrawl(%q) error = %v", seed, err)
	}
	return c
}

// --- Open / schema ---

func TestOpen_SchemaIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eanbot.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if _, err := s1.CreateCrawl("https://example.com", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("CreateCrawl() error = %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	defer s2.Close()

	crawls, err := s2.ListCrawls()
	if err != nil {
		t.Fatalf("ListCrawls() error = %v", err)
	}
	if len(crawls) != 1 {
		t.Fatalf("expected 1 crawl surviving reopen, got %d", len(crawls))
	}
}

func TestOpen_RunningCrawlBecomesFailed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eanbot.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	c := mustCreateCrawl(t, s1, "https://example.com")
	if c.Status != "running" {
		t.Fatalf("expected new crawl to be running, got %q", c.Status)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	defer s2.Close()

	got, err := s2.GetCrawl(c.ID)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("Status = %q, want failed", got.Status)
	}
	if got.Error != "interrumpido" {
		t.Errorf("Error = %q, want interrumpido", got.Error)
	}
	if got.FinishedAt == nil {
		t.Errorf("FinishedAt = nil, want non-nil")
	}
}

// TestOpen_MigratesOldSchema builds a database by hand using the schema as
// it existed before specs/003-store.md's "Contadores, lotes y checkpoints"
// (crawls without count_*/max_depth/duration_*/counters_ok, and no
// crawl_content_types table at all), inserts a crawl and some pages
// directly, then checks that Open upgrades it in place and Summarize
// reports counters recomputed from those pages.
func TestOpen_MigratesOldSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eanbot.db")

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	oldSchema := []string{
		`CREATE TABLE crawls (
			id          INTEGER PRIMARY KEY,
			seed        TEXT NOT NULL,
			status      TEXT NOT NULL,
			config      TEXT NOT NULL,
			started_at  TEXT NOT NULL,
			finished_at TEXT,
			pages_count INTEGER NOT NULL DEFAULT 0,
			error       TEXT NOT NULL DEFAULT '',
			robots_txt  TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE pages (
			id           INTEGER PRIMARY KEY,
			crawl_id     INTEGER NOT NULL REFERENCES crawls(id) ON DELETE CASCADE,
			url          TEXT NOT NULL,
			depth        INTEGER NOT NULL,
			status       INTEGER NOT NULL,
			content_type TEXT NOT NULL DEFAULT '',
			size         INTEGER NOT NULL DEFAULT 0,
			duration_ms  INTEGER NOT NULL DEFAULT 0,
			title        TEXT NOT NULL DEFAULT '',
			description  TEXT NOT NULL DEFAULT '',
			canonical    TEXT NOT NULL DEFAULT '',
			meta_robots  TEXT NOT NULL DEFAULT '',
			noindex      INTEGER NOT NULL DEFAULT 0,
			nofollow     INTEGER NOT NULL DEFAULT 0,
			h1           TEXT NOT NULL DEFAULT '',
			redirect_to  TEXT NOT NULL DEFAULT '',
			error        TEXT NOT NULL DEFAULT '',
			blocked      INTEGER NOT NULL DEFAULT 0,
			fetched_at   TEXT NOT NULL,
			UNIQUE (crawl_id, url)
		)`,
		`CREATE TABLE links (
			id           INTEGER PRIMARY KEY,
			crawl_id     INTEGER NOT NULL REFERENCES crawls(id) ON DELETE CASCADE,
			from_page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
			to_url       TEXT NOT NULL,
			text         TEXT NOT NULL DEFAULT '',
			nofollow     INTEGER NOT NULL DEFAULT 0,
			in_scope     INTEGER NOT NULL DEFAULT 0
		)`,
	}
	for _, stmt := range oldSchema {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("create old schema: %v", err)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	// pages_count deliberately wrong (0): backfill must recompute it from
	// pages, not trust whatever an old, possibly-stale value says.
	res, err := raw.Exec(
		`INSERT INTO crawls (seed, status, config, started_at, finished_at, pages_count) VALUES (?, 'done', '{}', ?, ?, 0)`,
		"https://example.com", now, now,
	)
	if err != nil {
		t.Fatalf("insert old crawl: %v", err)
	}
	crawlID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId() error = %v", err)
	}

	oldPages := []struct {
		url         string
		status      int
		blocked     int
		noindex     int
		depth       int
		contentType string
		durationMs  int64
	}{
		{"https://example.com/", 200, 0, 0, 0, "text/html", 100},
		{"https://example.com/a", 200, 0, 1, 1, "text/html", 200},
		{"https://example.com/missing", 404, 0, 0, 2, "text/html", 10},
		{"https://example.com/err", 0, 0, 0, 1, "", 0},
		{"https://example.com/blocked", 0, 1, 0, 1, "", 0},
	}
	for _, p := range oldPages {
		if _, err := raw.Exec(
			`INSERT INTO pages (crawl_id, url, depth, status, content_type, duration_ms, blocked, noindex, fetched_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			crawlID, p.url, p.depth, p.status, p.contentType, p.durationMs, p.blocked, p.noindex, now,
		); err != nil {
			t.Fatalf("insert old page %s: %v", p.url, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() on old schema error = %v", err)
	}
	defer s.Close()

	sum, err := s.Summarize(crawlID)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if sum.Total != 5 {
		t.Errorf("Total = %d, want 5", sum.Total)
	}
	if sum.Status2xx != 2 {
		t.Errorf("Status2xx = %d, want 2", sum.Status2xx)
	}
	if sum.Status4xx != 1 {
		t.Errorf("Status4xx = %d, want 1", sum.Status4xx)
	}
	if sum.Errors != 1 {
		t.Errorf("Errors = %d, want 1", sum.Errors)
	}
	if sum.Blocked != 1 {
		t.Errorf("Blocked = %d, want 1", sum.Blocked)
	}
	if sum.NoIndex != 1 {
		t.Errorf("NoIndex = %d, want 1", sum.NoIndex)
	}
	if sum.MaxDepth != 2 {
		t.Errorf("MaxDepth = %d, want 2", sum.MaxDepth)
	}
	if sum.ContentTypes["text/html"] != 3 {
		t.Errorf("ContentTypes[text/html] = %d, want 3", sum.ContentTypes["text/html"])
	}
	if sum.AvgDurationMs != 103 { // (100+200+10)/3, integer division
		t.Errorf("AvgDurationMs = %d, want 103", sum.AvgDurationMs)
	}

	got, err := s.GetCrawl(crawlID)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}
	if got.PagesCount != 5 {
		t.Errorf("PagesCount = %d, want 5 (recomputed, not the stale stored 0)", got.PagesCount)
	}

	var countersOK int
	if err := s.w.QueryRow(`SELECT counters_ok FROM crawls WHERE id = ?`, crawlID).Scan(&countersOK); err != nil {
		t.Fatalf("query counters_ok: %v", err)
	}
	if countersOK != 1 {
		t.Errorf("counters_ok = %d, want 1", countersOK)
	}

	// AddPages (and hence AddPage) must keep working against the migrated
	// schema: new columns and crawl_content_types must actually exist.
	if _, err := s.AddPage(
		Page{CrawlID: crawlID, URL: "https://example.com/new", Status: 200, ContentType: "text/html", FetchedAt: time.Now().UTC()},
		nil,
	); err != nil {
		t.Fatalf("AddPage() after migration error = %v", err)
	}
}

// --- CreateCrawl / GetCrawl / ListCrawls ---

func TestCreateCrawl_GetCrawl(t *testing.T) {
	s := openTestStore(t)

	cfg := json.RawMessage(`{"seed":"https://example.com","max_pages":10}`)
	before := time.Now().UTC()
	c, err := s.CreateCrawl("https://example.com", cfg)
	if err != nil {
		t.Fatalf("CreateCrawl() error = %v", err)
	}
	after := time.Now().UTC()

	if c.ID == 0 {
		t.Errorf("ID = 0, want non-zero")
	}
	if c.Seed != "https://example.com" {
		t.Errorf("Seed = %q", c.Seed)
	}
	if c.Status != "running" {
		t.Errorf("Status = %q, want running", c.Status)
	}
	if string(c.Config) != string(cfg) {
		t.Errorf("Config = %s, want %s", c.Config, cfg)
	}
	if c.StartedAt.Before(before.Add(-time.Second)) || c.StartedAt.After(after.Add(time.Second)) {
		t.Errorf("StartedAt = %v, want between %v and %v", c.StartedAt, before, after)
	}
	if c.FinishedAt != nil {
		t.Errorf("FinishedAt = %v, want nil", c.FinishedAt)
	}
	if c.PagesCount != 0 {
		t.Errorf("PagesCount = %d, want 0", c.PagesCount)
	}

	got, err := s.GetCrawl(c.ID)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}
	if got.ID != c.ID || got.Seed != c.Seed || got.Status != c.Status {
		t.Errorf("GetCrawl() = %+v, want %+v", got, c)
	}
}

func TestGetCrawl_NotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.GetCrawl(999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestListCrawls_MostRecentFirst(t *testing.T) {
	s := openTestStore(t)

	c1 := mustCreateCrawl(t, s, "https://a.example.com")
	c2 := mustCreateCrawl(t, s, "https://b.example.com")
	c3 := mustCreateCrawl(t, s, "https://c.example.com")

	got, err := s.ListCrawls()
	if err != nil {
		t.Fatalf("ListCrawls() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	wantOrder := []int64{c3.ID, c2.ID, c1.ID}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("got[%d].ID = %d, want %d", i, got[i].ID, id)
		}
	}
}

// --- SetRobots ---

func TestSetRobots(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")

	if err := s.SetRobots(c.ID, "User-agent: *\nDisallow: /admin"); err != nil {
		t.Fatalf("SetRobots() error = %v", err)
	}
	got, err := s.GetCrawl(c.ID)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}
	if got.RobotsTxt != "User-agent: *\nDisallow: /admin" {
		t.Errorf("RobotsTxt = %q", got.RobotsTxt)
	}
}

func TestSetRobots_NotFound(t *testing.T) {
	s := openTestStore(t)
	err := s.SetRobots(999, "x")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// --- FinishCrawl ---

func TestFinishCrawl_States(t *testing.T) {
	tests := []struct {
		name   string
		status string
		errMsg string
	}{
		{"done", "done", ""},
		{"failed", "failed", "boom"},
		{"cancelled", "cancelled", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openTestStore(t)
			c := mustCreateCrawl(t, s, "https://example.com")

			if err := s.FinishCrawl(c.ID, tt.status, tt.errMsg); err != nil {
				t.Fatalf("FinishCrawl() error = %v", err)
			}
			got, err := s.GetCrawl(c.ID)
			if err != nil {
				t.Fatalf("GetCrawl() error = %v", err)
			}
			if got.Status != tt.status {
				t.Errorf("Status = %q, want %q", got.Status, tt.status)
			}
			if got.Error != tt.errMsg {
				t.Errorf("Error = %q, want %q", got.Error, tt.errMsg)
			}
			if got.FinishedAt == nil {
				t.Errorf("FinishedAt = nil, want set")
			}
		})
	}
}

func TestFinishCrawl_Idempotent(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")

	if err := s.FinishCrawl(c.ID, "done", ""); err != nil {
		t.Fatalf("first FinishCrawl() error = %v", err)
	}
	first, err := s.GetCrawl(c.ID)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}

	// A second FinishCrawl on a non-running crawl must be a no-op, no error.
	if err := s.FinishCrawl(c.ID, "failed", "otro error"); err != nil {
		t.Fatalf("second FinishCrawl() error = %v", err)
	}
	second, err := s.GetCrawl(c.ID)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}
	if second.Status != first.Status || second.Error != first.Error {
		t.Errorf("crawl changed on second FinishCrawl: first=%+v second=%+v", first, second)
	}
}

func TestFinishCrawl_NotFound(t *testing.T) {
	s := openTestStore(t)
	err := s.FinishCrawl(999, "done", "")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// --- DeleteCrawl ---

func TestDeleteCrawl_Cascade(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")

	page := Page{
		CrawlID:   c.ID,
		URL:       "https://example.com/",
		Depth:     0,
		Status:    200,
		FetchedAt: time.Now().UTC(),
	}
	links := []Link{{ToURL: "https://example.com/a", Text: "a"}}
	pageID, err := s.AddPage(page, links)
	if err != nil {
		t.Fatalf("AddPage() error = %v", err)
	}

	if err := s.DeleteCrawl(c.ID); err != nil {
		t.Fatalf("DeleteCrawl() error = %v", err)
	}

	if _, err := s.GetCrawl(c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetCrawl() after delete err = %v, want ErrNotFound", err)
	}

	var pageCount, linkCount int
	if err := s.r.QueryRow(`SELECT COUNT(*) FROM pages WHERE crawl_id = ?`, c.ID).Scan(&pageCount); err != nil {
		t.Fatalf("count pages error = %v", err)
	}
	if pageCount != 0 {
		t.Errorf("pageCount = %d, want 0", pageCount)
	}
	if err := s.r.QueryRow(`SELECT COUNT(*) FROM links WHERE crawl_id = ?`, c.ID).Scan(&linkCount); err != nil {
		t.Fatalf("count links error = %v", err)
	}
	if linkCount != 0 {
		t.Errorf("linkCount = %d, want 0", linkCount)
	}
	_ = pageID
}

func TestDeleteCrawl_NotFound(t *testing.T) {
	s := openTestStore(t)
	err := s.DeleteCrawl(999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// --- AddPage / GetPage / FindPageByURL ---

func TestAddPage_IncrementsPagesCount(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")

	p1 := Page{CrawlID: c.ID, URL: "https://example.com/", Status: 200, FetchedAt: time.Now().UTC()}
	if _, err := s.AddPage(p1, nil); err != nil {
		t.Fatalf("AddPage() error = %v", err)
	}
	p2 := Page{CrawlID: c.ID, URL: "https://example.com/a", Status: 200, FetchedAt: time.Now().UTC()}
	if _, err := s.AddPage(p2, nil); err != nil {
		t.Fatalf("AddPage() error = %v", err)
	}

	got, err := s.GetCrawl(c.ID)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}
	if got.PagesCount != 2 {
		t.Errorf("PagesCount = %d, want 2", got.PagesCount)
	}
}

func TestAddPage_DuplicateURLRejected(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")

	p := Page{CrawlID: c.ID, URL: "https://example.com/", Status: 200, FetchedAt: time.Now().UTC()}
	if _, err := s.AddPage(p, nil); err != nil {
		t.Fatalf("first AddPage() error = %v", err)
	}
	if _, err := s.AddPage(p, nil); err == nil {
		t.Errorf("second AddPage() with duplicate URL error = nil, want error")
	}

	got, err := s.GetCrawl(c.ID)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}
	if got.PagesCount != 1 {
		t.Errorf("PagesCount = %d, want 1 (rejected insert must not increment)", got.PagesCount)
	}
}

// --- AddPages (batches) ---

func TestAddPages_UpdatesCountersAndContentTypes(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()

	batch := []PageWithLinks{
		{Page: Page{CrawlID: c.ID, URL: "https://example.com/", Status: 200, ContentType: "text/html", DurationMs: 100, Depth: 0, FetchedAt: now}},
		{Page: Page{CrawlID: c.ID, URL: "https://example.com/a", Status: 200, ContentType: "text/html", DurationMs: 200, Depth: 1, NoIndex: true, FetchedAt: now},
			Links: []Link{{ToURL: "https://example.com/b"}}},
		{Page: Page{CrawlID: c.ID, URL: "https://example.com/missing", Status: 404, ContentType: "text/html", DurationMs: 10, Depth: 2, FetchedAt: now}},
		{Page: Page{CrawlID: c.ID, URL: "https://example.com/broken", Status: 500, ContentType: "application/json", DurationMs: 30, Depth: 1, FetchedAt: now}},
		{Page: Page{CrawlID: c.ID, URL: "https://example.com/blocked", Status: 0, Blocked: true, Depth: 3, FetchedAt: now}},
		{Page: Page{CrawlID: c.ID, URL: "https://example.com/err", Status: 0, Depth: 1, FetchedAt: now}},
	}

	ids, err := s.AddPages(c.ID, batch)
	if err != nil {
		t.Fatalf("AddPages() error = %v", err)
	}
	if len(ids) != len(batch) {
		t.Fatalf("len(ids) = %d, want %d", len(ids), len(batch))
	}
	for i, id := range ids {
		if id == 0 {
			t.Errorf("ids[%d] = 0, want non-zero", i)
		}
	}

	got, err := s.GetCrawl(c.ID)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}
	if got.PagesCount != 6 {
		t.Errorf("PagesCount = %d, want 6", got.PagesCount)
	}

	sum, err := s.Summarize(c.ID)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if sum.Status2xx != 2 || sum.Status4xx != 1 || sum.Status5xx != 1 || sum.Errors != 1 || sum.Blocked != 1 {
		t.Errorf("Summary counts = %+v, want 2xx=2 4xx=1 5xx=1 errors=1 blocked=1", sum)
	}
	if sum.MaxDepth != 3 {
		t.Errorf("MaxDepth = %d, want 3", sum.MaxDepth)
	}
	if sum.ContentTypes["text/html"] != 3 || sum.ContentTypes["application/json"] != 1 {
		t.Errorf("ContentTypes = %+v, want text/html=3 application/json=1", sum.ContentTypes)
	}

	// A link inside the batch (pointing from /a to /b) must have landed too.
	detail, err := s.GetPage(c.ID, ids[1])
	if err != nil {
		t.Fatalf("GetPage() error = %v", err)
	}
	if len(detail.Outlinks) != 1 || detail.Outlinks[0].ToURL != "https://example.com/b" {
		t.Errorf("Outlinks = %+v, want one link to /b", detail.Outlinks)
	}
}

func TestAddPages_SecondBatchAccumulatesCounters(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()

	if _, err := s.AddPages(c.ID, []PageWithLinks{
		{Page: Page{CrawlID: c.ID, URL: "https://example.com/1", Status: 200, ContentType: "text/html", DurationMs: 100, FetchedAt: now}},
	}); err != nil {
		t.Fatalf("first AddPages() error = %v", err)
	}
	if _, err := s.AddPages(c.ID, []PageWithLinks{
		{Page: Page{CrawlID: c.ID, URL: "https://example.com/2", Status: 200, ContentType: "text/html", DurationMs: 300, FetchedAt: now}},
	}); err != nil {
		t.Fatalf("second AddPages() error = %v", err)
	}

	sum, err := s.Summarize(c.ID)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if sum.Total != 2 || sum.Status2xx != 2 {
		t.Errorf("Total/Status2xx = %d/%d, want 2/2", sum.Total, sum.Status2xx)
	}
	if sum.ContentTypes["text/html"] != 2 {
		t.Errorf("ContentTypes[text/html] = %d, want 2 (accumulated across batches)", sum.ContentTypes["text/html"])
	}
	if sum.AvgDurationMs != 200 { // (100+300)/2
		t.Errorf("AvgDurationMs = %d, want 200", sum.AvgDurationMs)
	}
}

// TestAddPages_DuplicateURLRollsBackWholeBatch checks that a duplicate URL
// anywhere in a batch (against an existing page, or against an earlier page
// in the same batch) rolls back every page, link and counter update from
// that batch -- not just the offending page.
func TestAddPages_DuplicateURLRollsBackWholeBatch(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()

	batch := []PageWithLinks{
		{Page: Page{CrawlID: c.ID, URL: "https://example.com/1", Status: 200, ContentType: "text/html", FetchedAt: now}},
		{Page: Page{CrawlID: c.ID, URL: "https://example.com/2", Status: 200, ContentType: "text/html", FetchedAt: now}},
		{Page: Page{CrawlID: c.ID, URL: "https://example.com/1", Status: 200, ContentType: "text/html", FetchedAt: now}}, // duplicate of the first
	}

	if _, err := s.AddPages(c.ID, batch); err == nil {
		t.Fatal("AddPages() with duplicate URL in batch error = nil, want error")
	}

	got, err := s.GetCrawl(c.ID)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}
	if got.PagesCount != 0 {
		t.Errorf("PagesCount = %d, want 0 (whole batch rolled back)", got.PagesCount)
	}
	sum, err := s.Summarize(c.ID)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if sum.Total != 0 || len(sum.ContentTypes) != 0 {
		t.Errorf("Summary = %+v, want empty (whole batch rolled back)", sum)
	}
	if _, err := s.FindPageByURL(c.ID, "https://example.com/2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindPageByURL(/2) err = %v, want ErrNotFound (rolled back)", err)
	}
}

// TestSummarize_MatchesDirectCountAfterSeveralBatches inserts a mix of
// codes, blocked pages, errors, noindex pages and content types across
// several AddPages batches, then checks that Summarize's O(1) counters
// agree with a direct COUNT(*)/aggregate over pages -- the whole point of
// keeping them in sync incrementally (see "Contadores" in
// specs/003-store.md).
func TestSummarize_MatchesDirectCountAfterSeveralBatches(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()

	statuses := []int{200, 200, 201, 301, 404, 404, 500, 0, 0}
	blocked := []bool{false, false, false, false, false, false, false, true, false}
	noindex := []bool{false, true, false, false, false, false, false, false, false}
	contentTypes := []string{"text/html", "text/html", "application/json", "", "text/html", "", "text/html", "", ""}
	depths := []int{0, 1, 2, 1, 3, 2, 1, 4, 2}

	batches := [][]int{{0, 1, 2}, {3, 4}, {5, 6, 7, 8}}
	for bi, idxs := range batches {
		var batch []PageWithLinks
		for _, i := range idxs {
			batch = append(batch, PageWithLinks{Page: Page{
				CrawlID:     c.ID,
				URL:         fmt.Sprintf("https://example.com/p%d", i),
				Status:      statuses[i],
				Blocked:     blocked[i],
				NoIndex:     noindex[i],
				ContentType: contentTypes[i],
				Depth:       depths[i],
				DurationMs:  int64(10 * (i + 1)),
				FetchedAt:   now,
			}})
		}
		if _, err := s.AddPages(c.ID, batch); err != nil {
			t.Fatalf("AddPages() batch %d error = %v", bi, err)
		}
	}

	sum, err := s.Summarize(c.ID)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}

	var wantTotal, want2xx, want3xx, want4xx, want5xx, wantErrors, wantBlocked, wantNoIndex, wantMaxDepth int
	var wantDurSum, wantDurN int64
	wantCT := map[string]int{}
	for i, st := range statuses {
		wantTotal++
		switch {
		case st >= 200 && st <= 299:
			want2xx++
		case st >= 300 && st <= 399:
			want3xx++
		case st >= 400 && st <= 499:
			want4xx++
		case st >= 500 && st <= 599:
			want5xx++
		case st == 0 && !blocked[i]:
			wantErrors++
		}
		if blocked[i] {
			wantBlocked++
		}
		if noindex[i] {
			wantNoIndex++
		}
		if depths[i] > wantMaxDepth {
			wantMaxDepth = depths[i]
		}
		if st > 0 {
			wantDurSum += int64(10 * (i + 1))
			wantDurN++
		}
		if contentTypes[i] != "" {
			wantCT[contentTypes[i]]++
		}
	}
	wantAvg := int64(0)
	if wantDurN > 0 {
		wantAvg = wantDurSum / wantDurN
	}

	if sum.Total != wantTotal || sum.Status2xx != want2xx || sum.Status3xx != want3xx ||
		sum.Status4xx != want4xx || sum.Status5xx != want5xx || sum.Errors != wantErrors ||
		sum.Blocked != wantBlocked || sum.NoIndex != wantNoIndex || sum.MaxDepth != wantMaxDepth ||
		sum.AvgDurationMs != wantAvg {
		t.Fatalf("Summarize() = %+v, want total=%d 2xx=%d 3xx=%d 4xx=%d 5xx=%d errors=%d blocked=%d noindex=%d maxdepth=%d avg=%d",
			sum, wantTotal, want2xx, want3xx, want4xx, want5xx, wantErrors, wantBlocked, wantNoIndex, wantMaxDepth, wantAvg)
	}
	for ct, n := range wantCT {
		if sum.ContentTypes[ct] != n {
			t.Errorf("ContentTypes[%q] = %d, want %d", ct, sum.ContentTypes[ct], n)
		}
	}

	// Direct recount over pages must agree with the incrementally
	// maintained counters.
	var directTotal int
	if err := s.r.QueryRow(`SELECT COUNT(*) FROM pages WHERE crawl_id = ?`, c.ID).Scan(&directTotal); err != nil {
		t.Fatalf("direct COUNT(*) error = %v", err)
	}
	if directTotal != sum.Total {
		t.Errorf("direct COUNT(*) = %d, Summarize().Total = %d, want equal", directTotal, sum.Total)
	}
	var direct2xx int
	if err := s.r.QueryRow(`SELECT COUNT(*) FROM pages WHERE crawl_id = ? AND status BETWEEN 200 AND 299`, c.ID).Scan(&direct2xx); err != nil {
		t.Fatalf("direct COUNT(*) 2xx error = %v", err)
	}
	if direct2xx != sum.Status2xx {
		t.Errorf("direct 2xx COUNT(*) = %d, Summarize().Status2xx = %d, want equal", direct2xx, sum.Status2xx)
	}
}

func TestGetPage_OutAndInLinks(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")

	home := Page{CrawlID: c.ID, URL: "https://example.com/", Status: 200, FetchedAt: time.Now().UTC()}
	homeID, err := s.AddPage(home, []Link{
		{ToURL: "https://example.com/a", Text: "A", InScope: true},
		{ToURL: "https://example.com/b", Text: "B", NoFollow: true, InScope: true},
	})
	if err != nil {
		t.Fatalf("AddPage(home) error = %v", err)
	}

	pageA := Page{CrawlID: c.ID, URL: "https://example.com/a", Status: 200, FetchedAt: time.Now().UTC()}
	pageAID, err := s.AddPage(pageA, []Link{
		{ToURL: "https://example.com/", Text: "home", InScope: true},
	})
	if err != nil {
		t.Fatalf("AddPage(a) error = %v", err)
	}

	detail, err := s.GetPage(c.ID, homeID)
	if err != nil {
		t.Fatalf("GetPage(home) error = %v", err)
	}
	if detail.Page.URL != home.URL {
		t.Errorf("URL = %q", detail.Page.URL)
	}
	if len(detail.Outlinks) != 2 {
		t.Fatalf("len(Outlinks) = %d, want 2", len(detail.Outlinks))
	}
	if detail.OutlinksTotal != 2 {
		t.Errorf("OutlinksTotal = %d, want 2", detail.OutlinksTotal)
	}
	if len(detail.Inlinks) != 1 {
		t.Fatalf("len(Inlinks) = %d, want 1", len(detail.Inlinks))
	}
	if detail.InlinksTotal != 1 {
		t.Errorf("InlinksTotal = %d, want 1", detail.InlinksTotal)
	}
	if detail.Inlinks[0].FromURL != pageA.URL {
		t.Errorf("Inlinks[0].FromURL = %q, want %q", detail.Inlinks[0].FromURL, pageA.URL)
	}
	if detail.Inlinks[0].FromPageID != pageAID {
		t.Errorf("Inlinks[0].FromPageID = %d, want %d", detail.Inlinks[0].FromPageID, pageAID)
	}
}

func TestGetPage_NotFound(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	_, err := s.GetPage(c.ID, 999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestGetPage_LinkLimit checks that outlinks and inlinks are capped at
// LinkLimit even when more exist, while the *_total fields report the true
// count.
func TestGetPage_LinkLimit(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()

	outLinks := make([]Link, 0, LinkLimit+1)
	for i := 0; i < LinkLimit+1; i++ {
		outLinks = append(outLinks, Link{ToURL: fmt.Sprintf("https://example.com/out%d", i)})
	}
	homeID, err := s.AddPage(Page{CrawlID: c.ID, URL: "https://example.com/", Status: 200, FetchedAt: now}, outLinks)
	if err != nil {
		t.Fatalf("AddPage(home) error = %v", err)
	}
	for i := 0; i < LinkLimit+1; i++ {
		url := fmt.Sprintf("https://example.com/in%d", i)
		if _, err := s.AddPage(Page{CrawlID: c.ID, URL: url, Status: 200, FetchedAt: now}, []Link{
			{ToURL: "https://example.com/"},
		}); err != nil {
			t.Fatalf("AddPage(%s) error = %v", url, err)
		}
	}

	detail, err := s.GetPage(c.ID, homeID)
	if err != nil {
		t.Fatalf("GetPage() error = %v", err)
	}
	if len(detail.Outlinks) != LinkLimit {
		t.Errorf("len(Outlinks) = %d, want %d", len(detail.Outlinks), LinkLimit)
	}
	if detail.OutlinksTotal != LinkLimit+1 {
		t.Errorf("OutlinksTotal = %d, want %d", detail.OutlinksTotal, LinkLimit+1)
	}
	if len(detail.Inlinks) != LinkLimit {
		t.Errorf("len(Inlinks) = %d, want %d", len(detail.Inlinks), LinkLimit)
	}
	if detail.InlinksTotal != LinkLimit+1 {
		t.Errorf("InlinksTotal = %d, want %d", detail.InlinksTotal, LinkLimit+1)
	}
}

func TestFindPageByURL(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	p := Page{CrawlID: c.ID, URL: "https://example.com/x", Status: 200, FetchedAt: time.Now().UTC()}
	id, err := s.AddPage(p, nil)
	if err != nil {
		t.Fatalf("AddPage() error = %v", err)
	}

	got, err := s.FindPageByURL(c.ID, "https://example.com/x")
	if err != nil {
		t.Fatalf("FindPageByURL() error = %v", err)
	}
	if got.ID != id {
		t.Errorf("ID = %d, want %d", got.ID, id)
	}
}

func TestFindPageByURL_NotFound(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	_, err := s.FindPageByURL(c.ID, "https://example.com/missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// --- ListPages ---

func seedListPagesCrawl(t *testing.T, s *Store) int64 {
	t.Helper()
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()
	pages := []Page{
		{CrawlID: c.ID, URL: "https://example.com/", Status: 200, Title: "Home", FetchedAt: now},
		{CrawlID: c.ID, URL: "https://example.com/redirect", Status: 301, Title: "Redirect", FetchedAt: now},
		{CrawlID: c.ID, URL: "https://example.com/missing", Status: 404, Title: "Not Found", FetchedAt: now},
		{CrawlID: c.ID, URL: "https://example.com/broken", Status: 500, Title: "Server Error", FetchedAt: now},
		{CrawlID: c.ID, URL: "https://example.com/error", Status: 0, Blocked: false, Error: "timeout", FetchedAt: now},
		{CrawlID: c.ID, URL: "https://example.com/blocked", Status: 0, Blocked: true, FetchedAt: now},
	}
	for _, p := range pages {
		if _, err := s.AddPage(p, nil); err != nil {
			t.Fatalf("AddPage(%s) error = %v", p.URL, err)
		}
	}
	return c.ID
}

func TestListPages_StatusFilters(t *testing.T) {
	tests := []struct {
		status  string
		wantLen int
	}{
		{"", 6},
		{"2xx", 1},
		{"3xx", 1},
		{"4xx", 1},
		{"5xx", 1},
		{"error", 1},
		{"blocked", 1},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			s := openTestStore(t)
			crawlID := seedListPagesCrawl(t, s)

			got, total, err := s.ListPages(crawlID, PageFilter{Status: tt.status})
			if err != nil {
				t.Fatalf("ListPages() error = %v", err)
			}
			if len(got) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(got), tt.wantLen)
			}
			if total != tt.wantLen {
				t.Errorf("total = %d, want %d", total, tt.wantLen)
			}
		})
	}
}

func TestListPages_InvalidStatus(t *testing.T) {
	s := openTestStore(t)
	crawlID := seedListPagesCrawl(t, s)
	_, _, err := s.ListPages(crawlID, PageFilter{Status: "6xx"})
	if err == nil {
		t.Errorf("err = nil, want error for invalid status")
	}
}

func TestListPages_Query(t *testing.T) {
	s := openTestStore(t)
	crawlID := seedListPagesCrawl(t, s)

	got, total, err := s.ListPages(crawlID, PageFilter{Query: "redirect"})
	if err != nil {
		t.Fatalf("ListPages() error = %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Fatalf("total=%d len=%d, want 1", total, len(got))
	}
	if got[0].URL != "https://example.com/redirect" {
		t.Errorf("URL = %q", got[0].URL)
	}

	// Query matches title too.
	got, total, err = s.ListPages(crawlID, PageFilter{Query: "Home"})
	if err != nil {
		t.Fatalf("ListPages() error = %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Fatalf("total=%d len=%d, want 1", total, len(got))
	}
}

func TestListPages_QueryEscapesWildcards(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()
	pages := []Page{
		{CrawlID: c.ID, URL: "https://example.com/100%off", Status: 200, FetchedAt: now},
		{CrawlID: c.ID, URL: "https://example.com/normal", Status: 200, FetchedAt: now},
	}
	for _, p := range pages {
		if _, err := s.AddPage(p, nil); err != nil {
			t.Fatalf("AddPage() error = %v", err)
		}
	}

	got, total, err := s.ListPages(c.ID, PageFilter{Query: "100%off"})
	if err != nil {
		t.Fatalf("ListPages() error = %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Fatalf("total=%d len=%d, want 1 (literal %% must not act as wildcard)", total, len(got))
	}
}

func TestListPages_LimitOffset(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		p := Page{CrawlID: c.ID, URL: "https://example.com/" + string(rune('a'+i)), Status: 200, FetchedAt: now}
		if _, err := s.AddPage(p, nil); err != nil {
			t.Fatalf("AddPage() error = %v", err)
		}
	}

	got, total, err := s.ListPages(c.ID, PageFilter{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("ListPages() error = %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// Ordered by id: skip first, take next two.
	if got[0].URL != "https://example.com/b" || got[1].URL != "https://example.com/c" {
		t.Errorf("got URLs = %q, %q", got[0].URL, got[1].URL)
	}
}

func TestListPages_DefaultAndMaxLimit(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()
	p := Page{CrawlID: c.ID, URL: "https://example.com/", Status: 200, FetchedAt: now}
	if _, err := s.AddPage(p, nil); err != nil {
		t.Fatalf("AddPage() error = %v", err)
	}

	// Limit 0 -> default 100 (not asserted directly, just must not error and
	// must return all rows). Limit above 1000 -> capped.
	_, _, err := s.ListPages(c.ID, PageFilter{Limit: 0})
	if err != nil {
		t.Fatalf("ListPages(limit=0) error = %v", err)
	}
	_, _, err = s.ListPages(c.ID, PageFilter{Limit: 5000})
	if err != nil {
		t.Fatalf("ListPages(limit=5000) error = %v", err)
	}
}

// --- Summarize ---

func TestSummarize(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()

	pages := []Page{
		{CrawlID: c.ID, URL: "https://example.com/", Status: 200, ContentType: "text/html", DurationMs: 100, Depth: 0, FetchedAt: now},
		{CrawlID: c.ID, URL: "https://example.com/a", Status: 200, ContentType: "text/html", DurationMs: 200, Depth: 1, NoIndex: true, FetchedAt: now},
		{CrawlID: c.ID, URL: "https://example.com/b", Status: 301, ContentType: "", DurationMs: 50, Depth: 1, FetchedAt: now},
		{CrawlID: c.ID, URL: "https://example.com/c", Status: 404, ContentType: "text/html", DurationMs: 10, Depth: 2, FetchedAt: now},
		{CrawlID: c.ID, URL: "https://example.com/d", Status: 500, ContentType: "application/json", DurationMs: 30, Depth: 2, FetchedAt: now},
		{CrawlID: c.ID, URL: "https://example.com/e", Status: 0, Blocked: false, Depth: 3, FetchedAt: now},
		{CrawlID: c.ID, URL: "https://example.com/f", Status: 0, Blocked: true, Depth: 1, FetchedAt: now},
	}
	for _, p := range pages {
		if _, err := s.AddPage(p, nil); err != nil {
			t.Fatalf("AddPage(%s) error = %v", p.URL, err)
		}
	}

	sum, err := s.Summarize(c.ID)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if sum.Total != 7 {
		t.Errorf("Total = %d, want 7", sum.Total)
	}
	if sum.Status2xx != 2 {
		t.Errorf("Status2xx = %d, want 2", sum.Status2xx)
	}
	if sum.Status3xx != 1 {
		t.Errorf("Status3xx = %d, want 1", sum.Status3xx)
	}
	if sum.Status4xx != 1 {
		t.Errorf("Status4xx = %d, want 1", sum.Status4xx)
	}
	if sum.Status5xx != 1 {
		t.Errorf("Status5xx = %d, want 1", sum.Status5xx)
	}
	if sum.Errors != 1 {
		t.Errorf("Errors = %d, want 1", sum.Errors)
	}
	if sum.Blocked != 1 {
		t.Errorf("Blocked = %d, want 1", sum.Blocked)
	}
	if sum.NoIndex != 1 {
		t.Errorf("NoIndex = %d, want 1", sum.NoIndex)
	}
	if sum.MaxDepth != 3 {
		t.Errorf("MaxDepth = %d, want 3", sum.MaxDepth)
	}
	wantCT := map[string]int{"text/html": 3, "application/json": 1}
	if len(sum.ContentTypes) != len(wantCT) {
		t.Errorf("ContentTypes = %+v, want %+v", sum.ContentTypes, wantCT)
	}
	for k, v := range wantCT {
		if sum.ContentTypes[k] != v {
			t.Errorf("ContentTypes[%q] = %d, want %d", k, sum.ContentTypes[k], v)
		}
	}
	// avg over status > 0: (100+200+50+10+30)/5 = 78
	if sum.AvgDurationMs != 78 {
		t.Errorf("AvgDurationMs = %d, want 78", sum.AvgDurationMs)
	}
}

func TestSummarize_EmptyCrawl(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")

	sum, err := s.Summarize(c.ID)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if sum.Total != 0 {
		t.Errorf("Total = %d, want 0", sum.Total)
	}
	if sum.MaxDepth != 0 {
		t.Errorf("MaxDepth = %d, want 0", sum.MaxDepth)
	}
	if sum.AvgDurationMs != 0 {
		t.Errorf("AvgDurationMs = %d, want 0", sum.AvgDurationMs)
	}
}

func TestSummarize_NotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.Summarize(999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// --- BrokenLinks ---

// seedBrokenLinksCrawl builds a crawl with two broken pages: the seed page
// itself (500, no referrers since nothing links to a crawl's seed) and
// /missing (404, linked three times from the seed). A blocked page must not
// count as broken.
func seedBrokenLinksCrawl(t *testing.T, s *Store) int64 {
	t.Helper()
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()

	if _, err := s.AddPage(Page{CrawlID: c.ID, URL: "https://example.com/", Status: 500, FetchedAt: now}, []Link{
		{ToURL: "https://example.com/missing", Text: "1"},
		{ToURL: "https://example.com/missing", Text: "2"},
		{ToURL: "https://example.com/missing", Text: "3"},
	}); err != nil {
		t.Fatalf("AddPage(seed) error = %v", err)
	}
	if _, err := s.AddPage(Page{CrawlID: c.ID, URL: "https://example.com/missing", Status: 404, FetchedAt: now}, nil); err != nil {
		t.Fatalf("AddPage(missing) error = %v", err)
	}
	// A blocked page (status 0, blocked=true) must NOT count as broken.
	if _, err := s.AddPage(Page{CrawlID: c.ID, URL: "https://example.com/blocked", Status: 0, Blocked: true, FetchedAt: now}, nil); err != nil {
		t.Fatalf("AddPage(blocked) error = %v", err)
	}
	return c.ID
}

func TestBrokenLinks_ReferrersCountAndOrder(t *testing.T) {
	s := openTestStore(t)
	crawlID := seedBrokenLinksCrawl(t, s)

	broken, total, err := s.BrokenLinks(crawlID, 0, 0)
	if err != nil {
		t.Fatalf("BrokenLinks() error = %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(broken) != 2 {
		t.Fatalf("len(broken) = %d, want 2", len(broken))
	}
	// Ordered by url: / before /missing.
	if broken[0].Page.URL != "https://example.com/" {
		t.Errorf("broken[0].Page.URL = %q", broken[0].Page.URL)
	}
	if broken[0].ReferrersCount != 0 {
		t.Errorf("broken[0].ReferrersCount = %d, want 0", broken[0].ReferrersCount)
	}
	if broken[1].Page.URL != "https://example.com/missing" {
		t.Errorf("broken[1].Page.URL = %q", broken[1].Page.URL)
	}
	if broken[1].ReferrersCount != 3 {
		t.Errorf("broken[1].ReferrersCount = %d, want 3", broken[1].ReferrersCount)
	}
}

func TestBrokenLinks_Pagination(t *testing.T) {
	s := openTestStore(t)
	crawlID := seedBrokenLinksCrawl(t, s)

	page1, total, err := s.BrokenLinks(crawlID, 1, 0)
	if err != nil {
		t.Fatalf("BrokenLinks(limit=1, offset=0) error = %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(page1) != 1 || page1[0].Page.URL != "https://example.com/" {
		t.Fatalf("page1 = %+v, want just /", page1)
	}

	page2, total, err := s.BrokenLinks(crawlID, 1, 1)
	if err != nil {
		t.Fatalf("BrokenLinks(limit=1, offset=1) error = %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(page2) != 1 || page2[0].Page.URL != "https://example.com/missing" {
		t.Fatalf("page2 = %+v, want just /missing", page2)
	}
}

func TestBrokenLinks_DefaultAndMaxLimit(t *testing.T) {
	s := openTestStore(t)
	crawlID := seedBrokenLinksCrawl(t, s)

	if _, _, err := s.BrokenLinks(crawlID, 0, 0); err != nil {
		t.Fatalf("BrokenLinks(limit=0) error = %v", err)
	}
	if _, _, err := s.BrokenLinks(crawlID, 5000, 0); err != nil {
		t.Fatalf("BrokenLinks(limit=5000) error = %v", err)
	}
}

func TestBrokenLinks_NotFound(t *testing.T) {
	s := openTestStore(t)
	_, _, err := s.BrokenLinks(999, 0, 0)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// --- Referrers ---

func TestReferrers_PerURLLimit(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()

	for i := 0; i < 5; i++ {
		url := fmt.Sprintf("https://example.com/from%d", i)
		if _, err := s.AddPage(Page{CrawlID: c.ID, URL: url, Status: 200, FetchedAt: now}, []Link{
			{ToURL: "https://example.com/missing", Text: fmt.Sprintf("link%d", i)},
		}); err != nil {
			t.Fatalf("AddPage(%s) error = %v", url, err)
		}
	}
	if _, err := s.AddPage(Page{CrawlID: c.ID, URL: "https://example.com/missing", Status: 404, FetchedAt: now}, nil); err != nil {
		t.Fatalf("AddPage(missing) error = %v", err)
	}

	got, err := s.Referrers(c.ID, []string{"https://example.com/missing"}, 3)
	if err != nil {
		t.Fatalf("Referrers() error = %v", err)
	}
	refs := got["https://example.com/missing"]
	if len(refs) != 3 {
		t.Fatalf("len(refs) = %d, want 3", len(refs))
	}
	for i, want := range []string{"from0", "from1", "from2"} {
		if !strings.HasSuffix(refs[i].FromURL, want) {
			t.Errorf("refs[%d].FromURL = %q, want suffix %q", i, refs[i].FromURL, want)
		}
	}
}

func TestReferrers_DefaultPerURL(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()

	for i := 0; i < 5; i++ {
		if _, err := s.AddPage(Page{CrawlID: c.ID, URL: fmt.Sprintf("https://example.com/from%d", i), Status: 200, FetchedAt: now}, []Link{
			{ToURL: "https://example.com/missing"},
		}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.Referrers(c.ID, []string{"https://example.com/missing"}, 0)
	if err != nil {
		t.Fatalf("Referrers() error = %v", err)
	}
	if len(got["https://example.com/missing"]) != 3 {
		t.Errorf("len = %d, want 3 (perURL<=0 defaults to 3)", len(got["https://example.com/missing"]))
	}
}

func TestReferrers_EmptyToURLs(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")

	got, err := s.Referrers(c.ID, nil, 3)
	if err != nil {
		t.Fatalf("Referrers() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0", len(got))
	}
}

// --- concurrent reads while a write transaction is open ---

func TestConcurrentRead_NotBlockedByOpenWriteTransaction(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")

	// Open (but do not commit) a write transaction directly on the write
	// pool, simulating a long crawl insert. BEGIN IMMEDIATE takes the
	// reserved lock right away, without needing an actual write statement.
	if _, err := s.w.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("BEGIN IMMEDIATE error = %v", err)
	}
	defer func() {
		if _, err := s.w.Exec("COMMIT"); err != nil {
			t.Errorf("COMMIT error = %v", err)
		}
	}()

	done := make(chan error, 1)
	go func() {
		_, err := s.GetCrawl(c.ID)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("GetCrawl() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("GetCrawl() did not return within 1s while a write transaction was open on the write pool")
	}
}

// --- Checkpoint ---

func TestCheckpoint_TruncateEmptiesWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eanbot.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer s.Close()

	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()
	for i := 0; i < 20; i++ {
		p := Page{CrawlID: c.ID, URL: fmt.Sprintf("https://example.com/%d", i), Status: 200, FetchedAt: now}
		if _, err := s.AddPage(p, nil); err != nil {
			t.Fatalf("AddPage() error = %v", err)
		}
	}

	if err := s.Checkpoint("TRUNCATE"); err != nil {
		t.Fatalf("Checkpoint(TRUNCATE) error = %v", err)
	}

	walPath := path + "-wal"
	info, err := os.Stat(walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return // no -wal file at all is also "0 bytes" for our purposes
		}
		t.Fatalf("stat %s: %v", walPath, err)
	}
	if info.Size() != 0 {
		t.Errorf("-wal size = %d, want 0 after TRUNCATE checkpoint", info.Size())
	}
}

func TestCheckpoint_InvalidMode(t *testing.T) {
	s := openTestStore(t)
	if err := s.Checkpoint("BOGUS"); err == nil {
		t.Error("Checkpoint(BOGUS) error = nil, want error")
	}
}

// TestCheckpoint_BackgroundLoopRuns shrinks the package-level
// checkpointInterval so the background goroutine started by Open runs
// several times within the test's lifetime, then checks that it actually
// ran PRAGMA wal_checkpoint(RESTART) periodically: writing the same number
// of pages, with pauses in between, leaves far fewer WAL frames
// uncheckpointed with a short interval than with checkpointing effectively
// disabled (a very long interval). RESTART does not shrink the WAL file
// itself (only TRUNCATE does, see TestCheckpoint_TruncateEmptiesWAL) -- it
// checkpoints frames into the database file and lets the WAL be reused from
// the start, which is exactly what keeps a long crawl's WAL from growing
// without bound between the TRUNCATE checkpoints FinishCrawl/DeleteCrawl/
// Close run.
func TestCheckpoint_BackgroundLoopRuns(t *testing.T) {
	walLogFrames := func(interval time.Duration) int {
		orig := checkpointInterval
		checkpointInterval = interval
		defer func() { checkpointInterval = orig }()

		dir := t.TempDir()
		s, err := Open(filepath.Join(dir, "eanbot.db"))
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		defer s.Close()

		c := mustCreateCrawl(t, s, "https://example.com")
		now := time.Now().UTC()
		for i := 0; i < 30; i++ {
			p := Page{CrawlID: c.ID, URL: fmt.Sprintf("https://example.com/%d", i), Status: 200, FetchedAt: now}
			if _, err := s.AddPage(p, nil); err != nil {
				t.Fatalf("AddPage() error = %v", err)
			}
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(30 * time.Millisecond) // let a final tick land

		var busy, log, checkpointed int
		if err := s.w.QueryRow("PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &log, &checkpointed); err != nil {
			t.Fatalf("PRAGMA wal_checkpoint(PASSIVE) error = %v", err)
		}
		return log
	}

	withFrequentCheckpoints := walLogFrames(10 * time.Millisecond)
	withoutCheckpointing := walLogFrames(time.Hour)

	if withFrequentCheckpoints >= withoutCheckpointing {
		t.Errorf("WAL frames left with a 10ms checkpoint interval (%d) not smaller than with checkpointing effectively disabled (%d)",
			withFrequentCheckpoints, withoutCheckpointing)
	}
}

func TestOpen_UnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_, err := Open(filepath.Join(dir, "ro.db"))
	if err == nil {
		t.Fatal("expected error opening a database in a read-only directory")
	}
	if !strings.Contains(err.Error(), "no es escribible") || !strings.Contains(err.Error(), "65532") {
		t.Fatalf("error should explain the unwritable directory and the Docker uid, got: %v", err)
	}
}

func TestEnsureSQLiteTempDir(t *testing.T) {
	writable := t.TempDir()
	dbDir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing")
	tests := []struct {
		name       string
		env        string // current SQLITE_TMPDIR value
		candidates []string
		want       string // expected SQLITE_TMPDIR after the call ("" = untouched)
	}{
		{"already set", "/already", []string{missing}, "/already"},
		{"a candidate is writable", "", []string{missing, writable}, ""},
		{"no candidate is writable", "", []string{missing, "", "/proc"}, dbDir},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SQLITE_TMPDIR", tc.env)
			ensureSQLiteTempDir(dbDir, tc.candidates)
			if got := os.Getenv("SQLITE_TMPDIR"); got != tc.want {
				t.Fatalf("SQLITE_TMPDIR = %q, want %q", got, tc.want)
			}
		})
	}
}
