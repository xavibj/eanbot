package store

import (
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
