package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"xavi.net/eanbot/report"
	"xavi.net/eanbot/store"
)

// --- test helpers ---

func tempDBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "eanbot.db")
}

// newCrawlServer builds an httptest.Server serving:
//   - /robots.txt: disallows /blocked
//   - /: an HTML page linking to /page2, /blocked and /missing
//   - /page2: an HTML page linking back to /
//   - /blocked: would 200 if fetched, but robots.txt forbids it
//   - /missing: 404
//
// A crawl against it should visit 3 pages (/, /page2, /missing), block one
// (/blocked) and record one broken link (/missing, referenced from /).
func newCrawlServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "User-agent: *\nDisallow: /blocked\n")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Inicio</title></head><body>
			<a href="/page2">page2</a>
			<a href="/blocked">blocked</a>
			<a href="/missing">missing</a>
		</body></html>`)
	})
	mux.HandleFunc("/page2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Página 2</title></head><body><a href="/">home</a></body></html>`)
	})
	mux.HandleFunc("/blocked", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>should never be fetched</body></html>`)
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	return httptest.NewServer(mux)
}

func runCrawl(t *testing.T, srv *httptest.Server, dbPath string, extraArgs ...string) (int, string, string) {
	t.Helper()
	args := append([]string{"crawl", srv.URL, "-db", dbPath, "-concurrency", "1", "-delay", "0"}, extraArgs...)
	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

type crawlStatsJSON struct {
	Fetched  int `json:"fetched"`
	Blocked  int `json:"blocked"`
	Errors   int `json:"errors"`
	Queued   int `json:"queued"`
	Sitemaps int `json:"sitemaps"`
}

type crawlJSONOutput struct {
	Crawl   store.Crawl    `json:"crawl"`
	Summary store.Summary  `json:"summary"`
	Stats   crawlStatsJSON `json:"stats"`
}

// --- top-level dispatch ---

func TestRunNoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), nil, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if stderr.Len() == 0 {
		t.Error("expected usage on stderr")
	}
	if stdout.Len() != 0 {
		t.Errorf("expected nothing on stdout, got %q", stdout.String())
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{"bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "bogus") {
		t.Errorf("stderr = %q, want mention of unknown command", stderr.String())
	}
}

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if stdout.String() != "eanbot 0.1.0\n" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "eanbot 0.1.0\n")
	}
}

func TestRunHelp(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		var stdout, stderr bytes.Buffer
		code := runCtx(context.Background(), args, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("args=%v code = %d, want 0", args, code)
		}
		if !strings.Contains(stdout.String(), "eanbot crawl") {
			t.Errorf("args=%v stdout = %q, want usage", args, stdout.String())
		}
	}
}

// --- crawl ---

func TestCrawlInvalidFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{"crawl", "https://example.com", "-bogus-flag"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2, stderr = %s", code, stderr.String())
	}
}

func TestCrawlValidationErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dbPath := tempDBPath(t)
	code := runCtx(context.Background(), []string{"crawl", "not-a-url", "-db", dbPath, "-max-pages", "0"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "semilla") {
		t.Errorf("stderr = %q, want mention of seed validation error", stderr.String())
	}
	if !strings.Contains(stderr.String(), "max_pages") {
		t.Errorf("stderr = %q, want mention of max_pages validation error", stderr.String())
	}
}

func TestCrawlMissingSeed(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{"crawl", "-db", tempDBPath(t)}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2, stderr = %s", code, stderr.String())
	}
}

func TestCrawlHeaderFlagWithoutColon(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{
		"crawl", "https://example.com", "-db", tempDBPath(t), "-header", "sin-dos-puntos",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2, stderr = %s", code, stderr.String())
	}
	want := `cabecera sin ':': "sin-dos-puntos"`
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
}

func TestCrawlHeaderFlagSentToServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			return
		}
		if r.Header.Get("X-Ean-Client") != "abc" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>ok</body></html>`)
	}))
	defer srv.Close()

	code, stdout, stderr := runCrawl(t, srv, tempDBPath(t), "-max-pages", "1", "-json", "-quiet",
		"-header", "X-Ean-Client: abc")
	if code != 0 {
		t.Fatalf("code = %d, want 0, stderr = %s", code, stderr)
	}
	var out crawlJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", stdout, err)
	}
	if out.Summary.Status2xx != 1 {
		t.Errorf("with -header: Summary.Status2xx = %d, want 1 (got status %d)", out.Summary.Status2xx, out.Summary.Status4xx)
	}

	code, stdout, stderr = runCrawl(t, srv, tempDBPath(t), "-max-pages", "1", "-json", "-quiet")
	if code != 0 {
		t.Fatalf("code = %d, want 0 (a 403 page is still a completed crawl), stderr = %s", code, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", stdout, err)
	}
	if out.Summary.Status4xx != 1 {
		t.Errorf("without -header: Summary.Status4xx = %d, want 1", out.Summary.Status4xx)
	}
}

func TestCrawlHeaderFlagStoredInConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	code, stdout, stderr := runCrawl(t, srv, tempDBPath(t), "-max-pages", "1", "-json", "-quiet",
		"-header", "X-Ean-Client: abc")
	if code != 0 {
		t.Fatalf("code = %d, want 0, stderr = %s", code, stderr)
	}
	var out crawlJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", stdout, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(out.Crawl.Config, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	headers, ok := cfg["headers"].(map[string]any)
	if !ok {
		t.Fatalf("config[\"headers\"] = %v (%T), want an object", cfg["headers"], cfg["headers"])
	}
	if headers["X-Ean-Client"] != "abc" {
		t.Errorf("stored headers = %v, want X-Ean-Client=abc", headers)
	}
}

func TestCrawlNoHeaderFlagStoredAsEmptyObject(t *testing.T) {
	srv := newCrawlServer()
	defer srv.Close()

	code, stdout, stderr := runCrawl(t, srv, tempDBPath(t), "-max-pages", "1", "-json", "-quiet")
	if code != 0 {
		t.Fatalf("code = %d, want 0, stderr = %s", code, stderr)
	}
	var out crawlJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", stdout, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(out.Crawl.Config, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	headers, ok := cfg["headers"].(map[string]any)
	if !ok {
		t.Fatalf("config[\"headers\"] = %v (%T), want an object", cfg["headers"], cfg["headers"])
	}
	if len(headers) != 0 {
		t.Errorf("headers = %v, want empty object", headers)
	}
}

func TestCrawlOriginFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "sitio.test" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>ok</body></html>`)
	}))
	defer srv.Close()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort error: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{
		"crawl", "http://sitio.test/", "-db", tempDBPath(t),
		"-origin", "127.0.0.1:" + port, "-ignore-robots", "-max-pages", "1", "-json", "-quiet",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0, stderr = %s", code, stderr.String())
	}

	var out crawlJSONOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", stdout.String(), err)
	}
	if out.Summary.Status2xx != 1 {
		t.Errorf("Summary.Status2xx = %d, want 1 (-origin did not reach sitio.test)", out.Summary.Status2xx)
	}

	var cfg map[string]any
	if err := json.Unmarshal(out.Crawl.Config, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if cfg["origin"] != "127.0.0.1:"+port {
		t.Errorf("stored origin = %v, want %q", cfg["origin"], "127.0.0.1:"+port)
	}
}

func TestCrawlInvalidOriginFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{
		"crawl", "https://example.com/", "-db", tempDBPath(t), "-origin", "abc",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2, stderr = %s", code, stderr.String())
	}
	want := `origin no válido: "abc"`
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
}

func TestCrawlSuccessAndSummary(t *testing.T) {
	srv := newCrawlServer()
	defer srv.Close()
	dbPath := tempDBPath(t)

	code, stdout, stderr := runCrawl(t, srv, dbPath, "-quiet")
	if code != 0 {
		t.Fatalf("code = %d, want 0, stderr = %s", code, stderr)
	}

	if !strings.Contains(stdout, "Rastreo #1") {
		t.Errorf("stdout missing crawl header: %q", stdout)
	}
	if !strings.Contains(stdout, "Páginas: 4") {
		t.Errorf("stdout missing page count: %q", stdout)
	}
	if !strings.Contains(stdout, "2xx: 2") {
		t.Errorf("stdout missing 2xx count: %q", stdout)
	}
	if !strings.Contains(stdout, "4xx: 1") {
		t.Errorf("stdout missing 4xx count: %q", stdout)
	}
	if !strings.Contains(stdout, "Bloqueadas: 1") {
		t.Errorf("stdout missing blocked count: %q", stdout)
	}
	if !strings.Contains(stdout, "Enlaces rotos: 1") {
		t.Errorf("stdout missing broken link count: %q", stdout)
	}

	// -quiet must not suppress the final summary, only per-page progress.
	if strings.Contains(stderr, "[") {
		t.Errorf("stderr should have no progress lines with -quiet, got %q", stderr)
	}

	// Rows must actually be in the store.
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()
	pages, total, err := st.ListPages(1, store.PageFilter{})
	if err != nil {
		t.Fatalf("ListPages() error = %v", err)
	}
	if total != 4 || len(pages) != 4 {
		t.Errorf("ListPages() total = %d, len = %d, want 4", total, len(pages))
	}
}

func TestCrawlProgressLines(t *testing.T) {
	srv := newCrawlServer()
	defer srv.Close()
	dbPath := tempDBPath(t)

	code, _, stderr := runCrawl(t, srv, dbPath)
	if code != 0 {
		t.Fatalf("code = %d, want 0, stderr = %s", code, stderr)
	}
	if !strings.Contains(stderr, "[   1]") {
		t.Errorf("stderr missing progress line, got %q", stderr)
	}
}

func TestCrawlJSON(t *testing.T) {
	srv := newCrawlServer()
	defer srv.Close()
	dbPath := tempDBPath(t)

	code, stdout, stderr := runCrawl(t, srv, dbPath, "-json", "-quiet")
	if code != 0 {
		t.Fatalf("code = %d, want 0, stderr = %s", code, stderr)
	}

	var out crawlJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", stdout, err)
	}
	if out.Crawl.Status != "done" {
		t.Errorf("Crawl.Status = %q, want done", out.Crawl.Status)
	}
	if out.Summary.Total != 4 {
		t.Errorf("Summary.Total = %d, want 4", out.Summary.Total)
	}
	if out.Stats.Fetched != 3 {
		t.Errorf("Stats.Fetched = %d, want 3", out.Stats.Fetched)
	}
	if out.Stats.Blocked != 1 {
		t.Errorf("Stats.Blocked = %d, want 1", out.Stats.Blocked)
	}
	if out.Stats.Queued != 0 {
		t.Errorf("Stats.Queued = %d, want 0", out.Stats.Queued)
	}
}

func TestCrawlCancelledMidFlight(t *testing.T) {
	var once sync.Once
	started := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	dbPath := tempDBPath(t)
	var stdout, stderr bytes.Buffer
	args := []string{"crawl", srv.URL, "-db", dbPath, "-concurrency", "1", "-delay", "0", "-quiet"}
	code := runCtx(ctx, args, &stdout, &stderr)
	if code != 130 {
		t.Fatalf("code = %d, want 130, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "cancelled") {
		t.Errorf("stdout = %q, want mention of cancelled status", stdout.String())
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer st.Close()
	c, err := st.GetCrawl(1)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}
	if c.Status != "cancelled" {
		t.Errorf("crawl status = %q, want cancelled", c.Status)
	}
}

// --- crawls / pages / broken ---

func TestCrawlsEmptyTable(t *testing.T) {
	dbPath := tempDBPath(t)
	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{"crawls", "-db", dbPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ID") {
		t.Errorf("stdout = %q, want header row", stdout.String())
	}
}

func TestQueryCommandsAfterCrawl(t *testing.T) {
	srv := newCrawlServer()
	defer srv.Close()
	dbPath := tempDBPath(t)

	if code, _, stderr := runCrawl(t, srv, dbPath, "-quiet"); code != 0 {
		t.Fatalf("crawl code = %d, stderr = %s", code, stderr)
	}

	t.Run("crawls table", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runCtx(context.Background(), []string{"crawls", "-db", dbPath}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), srv.URL) {
			t.Errorf("stdout = %q, want seed URL", stdout.String())
		}
	})

	t.Run("crawls json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runCtx(context.Background(), []string{"crawls", "-db", dbPath, "-json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %s", code, stderr.String())
		}
		var out struct {
			Crawls []store.Crawl `json:"crawls"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if len(out.Crawls) != 1 {
			t.Fatalf("len(Crawls) = %d, want 1", len(out.Crawls))
		}
	})

	t.Run("pages status 4xx", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runCtx(context.Background(), []string{"pages", "1", "-db", dbPath, "-status", "4xx"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "/missing") {
			t.Errorf("stdout = %q, want /missing", stdout.String())
		}
		if strings.Contains(stdout.String(), "page2") {
			t.Errorf("stdout = %q, should not contain 2xx pages", stdout.String())
		}
	})

	t.Run("pages json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runCtx(context.Background(), []string{"pages", "1", "-db", dbPath, "-json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %s", code, stderr.String())
		}
		var out struct {
			Pages []store.Page `json:"pages"`
			Total int          `json:"total"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if out.Total != 4 {
			t.Errorf("Total = %d, want 4", out.Total)
		}
	})

	t.Run("pages not found", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runCtx(context.Background(), []string{"pages", "999", "-db", dbPath}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "no encontrado") {
			t.Errorf("stderr = %q, want 'no encontrado'", stderr.String())
		}
	})

	t.Run("pages non-numeric id", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runCtx(context.Background(), []string{"pages", "abc", "-db", dbPath}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
	})

	t.Run("broken", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runCtx(context.Background(), []string{"broken", "1", "-db", dbPath}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "/missing") {
			t.Errorf("stdout = %q, want /missing", stdout.String())
		}
		// "código  nº-referrers  URL" then, indented, "<- from_url (\"texto\")".
		if !strings.Contains(stdout.String(), "404  1  ") {
			t.Errorf("stdout = %q, want a '404  1  ' summary line", stdout.String())
		}
		if !strings.Contains(stdout.String(), fmt.Sprintf("    <- %s/ (\"missing\")", srv.URL)) {
			t.Errorf("stdout = %q, want an indented referrer line", stdout.String())
		}
	})

	t.Run("broken json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runCtx(context.Background(), []string{"broken", "1", "-db", dbPath, "-json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %s", code, stderr.String())
		}
		var out struct {
			Broken []brokenPageJSON `json:"broken"`
			Total  int              `json:"total"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if out.Total != 1 {
			t.Fatalf("Total = %d, want 1", out.Total)
		}
		if len(out.Broken) != 1 {
			t.Fatalf("len(Broken) = %d, want 1", len(out.Broken))
		}
		if !strings.HasSuffix(out.Broken[0].Page.URL, "/missing") {
			t.Errorf("Broken[0].Page.URL = %q, want suffix /missing", out.Broken[0].Page.URL)
		}
		if out.Broken[0].ReferrersCount != 1 {
			t.Errorf("Broken[0].ReferrersCount = %d, want 1", out.Broken[0].ReferrersCount)
		}
		if len(out.Broken[0].Referrers) != 1 || out.Broken[0].Referrers[0].FromURL != srv.URL+"/" {
			t.Errorf("Broken[0].Referrers = %+v, want one referrer from %s/", out.Broken[0].Referrers, srv.URL)
		}
	})

	t.Run("broken not found", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runCtx(context.Background(), []string{"broken", "999", "-db", dbPath}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("code = %d, want 1", code)
		}
	})
}

// TestCmdBroken_ReferrersFlag checks that -referrers caps the number of
// referring links shown per broken page while referrers_count still
// reports the true total, seeding the store directly (rather than via a
// crawl) to control the exact number of referrers.
func TestCmdBroken_ReferrersFlag(t *testing.T) {
	dbPath := tempDBPath(t)
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	crawl, err := st.CreateCrawl("https://example.com", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateCrawl() error = %v", err)
	}
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		if _, err := st.AddPage(store.Page{
			CrawlID: crawl.ID, URL: fmt.Sprintf("https://example.com/from%d", i), Status: 200, FetchedAt: now,
		}, []store.Link{{ToURL: "https://example.com/missing", Text: fmt.Sprintf("link%d", i)}}); err != nil {
			t.Fatalf("AddPage() error = %v", err)
		}
	}
	if _, err := st.AddPage(store.Page{
		CrawlID: crawl.ID, URL: "https://example.com/missing", Status: 404, FetchedAt: now,
	}, nil); err != nil {
		t.Fatalf("AddPage(missing) error = %v", err)
	}
	if err := st.FinishCrawl(crawl.ID, "done", ""); err != nil {
		t.Fatalf("FinishCrawl() error = %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{
		"broken", strconv.FormatInt(crawl.ID, 10), "-db", dbPath, "-referrers", "2", "-json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}

	var out struct {
		Broken []brokenPageJSON `json:"broken"`
		Total  int              `json:"total"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if out.Total != 1 || len(out.Broken) != 1 {
		t.Fatalf("out = %+v, want a single broken page", out)
	}
	if out.Broken[0].ReferrersCount != 5 {
		t.Errorf("ReferrersCount = %d, want 5", out.Broken[0].ReferrersCount)
	}
	if len(out.Broken[0].Referrers) != 2 {
		t.Errorf("len(Referrers) = %d, want 2 (capped by -referrers)", len(out.Broken[0].Referrers))
	}
}

// --- serve ---

func TestServeBanner(t *testing.T) {
	orig := listenAndServe
	defer func() { listenAndServe = orig }()

	var gotAddr string
	var gotHandler http.Handler
	listenAndServe = func(ctx context.Context, addr string, h http.Handler) error {
		gotAddr = addr
		gotHandler = h
		return nil
	}

	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{"serve", "-db", tempDBPath(t)}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "eanbot escuchando en http://localhost:8345") {
		t.Errorf("stdout = %q, want banner with localhost:8345", stdout.String())
	}
	if gotAddr != ":8345" {
		t.Errorf("addr = %q, want :8345", gotAddr)
	}
	if gotHandler == nil {
		t.Error("handler passed to listenAndServe was nil")
	}
}

func TestServeCustomAddr(t *testing.T) {
	orig := listenAndServe
	defer func() { listenAndServe = orig }()
	listenAndServe = func(ctx context.Context, addr string, h http.Handler) error { return nil }

	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{"serve", "-db", tempDBPath(t), "-addr", "0.0.0.0:9000"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "http://0.0.0.0:9000") {
		t.Errorf("stdout = %q, want banner with 0.0.0.0:9000", stdout.String())
	}
}

func TestServeError(t *testing.T) {
	orig := listenAndServe
	defer func() { listenAndServe = orig }()
	listenAndServe = func(ctx context.Context, addr string, h http.Handler) error {
		return errors.New("bind: address already in use")
	}

	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{"serve", "-db", tempDBPath(t)}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "bind: address already in use") {
		t.Errorf("stderr = %q, want the listenAndServe error", stderr.String())
	}
}

// --- report (spec 008) ---

// seedReportDB writes a small finished crawl straight to a fresh database
// and returns its path and crawl id.
func seedReportDB(t *testing.T) (string, int64) {
	t.Helper()
	dbPath := tempDBPath(t)
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	crawl, err := st.CreateCrawl("https://informe.example/", json.RawMessage(`{"seed":"https://informe.example/","max_pages":10}`))
	if err != nil {
		t.Fatalf("CreateCrawl() error = %v", err)
	}
	now := time.Now().UTC()
	pages := []store.PageWithLinks{
		{Page: store.Page{CrawlID: crawl.ID, URL: "https://informe.example/", Status: 200, ContentType: "text/html",
			Title: "Inicio", DurationMs: 40, Size: 1200, FetchedAt: now},
			Links: []store.Link{{ToURL: "https://informe.example/roto", InScope: true}}},
		{Page: store.Page{CrawlID: crawl.ID, URL: "https://informe.example/es/vieja", Depth: 1, Status: 301,
			RedirectTo: "https://informe.example/es/vieja/", DurationMs: 10, FetchedAt: now}},
		{Page: store.Page{CrawlID: crawl.ID, URL: "https://informe.example/roto", Depth: 1, Status: 404,
			DurationMs: 15, FetchedAt: now}},
	}
	if _, err := st.AddPages(crawl.ID, pages); err != nil {
		t.Fatalf("AddPages() error = %v", err)
	}
	if err := st.FinishCrawl(crawl.ID, "done", ""); err != nil {
		t.Fatalf("FinishCrawl() error = %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return dbPath, crawl.ID
}

func TestCmdReport_Markdown(t *testing.T) {
	dbPath, id := seedReportDB(t)

	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{"report", strconv.FormatInt(id, 10), "-db", dbPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}

	out := stdout.String()
	if !strings.HasPrefix(out, fmt.Sprintf("# Informe del rastreo #%d — https://informe.example/", id)) {
		t.Errorf("stdout does not start with the report title:\n%s", out)
	}
	for _, heading := range []string{"## Códigos", "## Redirecciones", "## Páginas rotas con más referrers", "## Muestras"} {
		if !strings.Contains(out, heading) {
			t.Errorf("stdout has no %q section", heading)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestCmdReport_JSON(t *testing.T) {
	dbPath, id := seedReportDB(t)

	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{"report", strconv.FormatInt(id, 10), "-db", dbPath, "-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}

	var rep report.Report
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", stdout.String(), err)
	}
	if rep.Crawl.ID != id {
		t.Errorf("crawl.id = %d, want %d", rep.Crawl.ID, id)
	}
	if rep.Summary.Total != 3 {
		t.Errorf("summary.total = %d, want 3", rep.Summary.Total)
	}
	if len(rep.Redirects) != 1 || rep.Redirects[0].Pattern != "añade barra final" {
		t.Errorf("redirects = %+v, want a single trailing-slash pattern", rep.Redirects)
	}
	if len(rep.TopBroken) != 1 || rep.TopBroken[0].Referrers != 1 {
		t.Errorf("top_broken = %+v, want the 404 with one referrer", rep.TopBroken)
	}
}

func TestCmdReport_OutputFile(t *testing.T) {
	dbPath, id := seedReportDB(t)
	out := filepath.Join(t.TempDir(), "informe.md")

	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{
		"report", strconv.FormatInt(id, 10), "-db", dbPath, "-o", out,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing when writing to a file", stdout.String())
	}
	if want := "informe escrito en " + out; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "## Códigos") {
		t.Errorf("written file is not the Markdown report:\n%s", data)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("file mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestCmdReport_OutputFileJSON(t *testing.T) {
	dbPath, id := seedReportDB(t)
	out := filepath.Join(t.TempDir(), "informe.json")

	var stdout, stderr bytes.Buffer
	code := runCtx(context.Background(), []string{
		"report", strconv.FormatInt(id, 10), "-db", dbPath, "-json", "-o", out,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var rep report.Report
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("written file is not valid JSON: %v", err)
	}
	if rep.Crawl.ID != id {
		t.Errorf("crawl.id = %d, want %d", rep.Crawl.ID, id)
	}
}

func TestCmdReport_NotFound(t *testing.T) {
	dbPath, _ := seedReportDB(t)

	tests := []struct{ name, id string }{
		{"id inexistente", "999"},
		{"id no numérico", "abc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runCtx(context.Background(), []string{"report", tc.id, "-db", dbPath}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("code = %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), "error: rastreo no encontrado") {
				t.Errorf("stderr = %q, want \"error: rastreo no encontrado\"", stderr.String())
			}
		})
	}
}

func TestCmdReport_MissingID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCtx(context.Background(), []string{"report"}, &stdout, &stderr); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestUsage_MentionsReport(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCtx(context.Background(), []string{"help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "eanbot report <crawl-id>") {
		t.Errorf("usage does not document the report subcommand:\n%s", stdout.String())
	}
}
