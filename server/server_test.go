package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"xavi.net/eanbot/crawler"
	"xavi.net/eanbot/store"
)

// --- test helpers: store, fake fetcher, HTTP ---

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "eanbot.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close() error = %v", err)
		}
	})
	return st
}

// fakeFetcher is an in-memory crawler.Fetcher: responses are looked up by
// exact URL, an unconfigured URL yields a plain 404 (which also makes
// robots.txt resolve to "allow everything" per crawler's own rules), and a
// URL marked slow blocks until the request's context is cancelled — used
// to exercise crawl cancellation without any timing-based flakiness.
type fakeFetcher struct {
	mu        sync.Mutex
	responses map[string]*crawler.Response
	errs      map[string]error
	slow      map[string]bool
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{
		responses: map[string]*crawler.Response{},
		errs:      map[string]error{},
		slow:      map[string]bool{},
	}
}

func (f *fakeFetcher) set(url string, resp *crawler.Response) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[url] = resp
}

func (f *fakeFetcher) setErr(url string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[url] = err
}

func (f *fakeFetcher) setSlow(url string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slow[url] = true
}

func (f *fakeFetcher) Fetch(ctx context.Context, url string) (*crawler.Response, error) {
	f.mu.Lock()
	slow := f.slow[url]
	resp, hasResp := f.responses[url]
	err, hasErr := f.errs[url]
	f.mu.Unlock()

	if slow {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if hasErr {
		return nil, err
	}
	if hasResp {
		return resp, nil
	}
	return &crawler.Response{Status: 404}, nil
}

func newFakeFetcherFactory(f crawler.Fetcher) func(crawler.Config) crawler.Fetcher {
	return func(crawler.Config) crawler.Fetcher { return f }
}

func htmlResponse(body string) *crawler.Response {
	return &crawler.Response{
		Status:      200,
		ContentType: "text/html; charset=utf-8",
		Body:        []byte(body),
		Size:        int64(len(body)),
	}
}

func statusResponse(status int) *crawler.Response {
	return &crawler.Response{Status: status}
}

// newTestServer builds a Server backed by a fresh temp-dir store and f,
// registering cleanup that shuts the Manager down (cancelling and waiting
// for any crawl still running) before the store itself is closed.
func newTestServer(t *testing.T, f crawler.Fetcher) (*Server, http.Handler) {
	t.Helper()
	st := newTestStore(t)
	srv := New(st, Options{NewFetcher: newFakeFetcherFactory(f)})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})
	return srv, srv.Handler()
}

func doRequest(h http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return b
}

func decodeJSON[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", w.Body.String(), err)
	}
	return v
}

type errorsBody struct {
	Errors []string `json:"errors"`
}

type crawlDetail struct {
	Crawl   store.Crawl   `json:"crawl"`
	Summary store.Summary `json:"summary"`
	Running bool          `json:"running"`
}

// waitUntilDone polls GET /api/crawls/{id} until running is false.
func waitUntilDone(t *testing.T, h http.Handler, id int64, timeout time.Duration) crawlDetail {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		w := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d", id), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/crawls/%d status = %d, body = %s", id, w.Code, w.Body.String())
		}
		detail := decodeJSON[crawlDetail](t, w)
		if !detail.Running {
			return detail
		}
		if time.Now().After(deadline) {
			t.Fatalf("crawl %d did not finish within %s", id, timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// --- tests ---

func TestHealthz(t *testing.T) {
	_, h := newTestServer(t, newFakeFetcher())

	w := doRequest(h, http.MethodGet, "/api/healthz", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if body := w.Body.String(); body != "{\"ok\":true}\n" {
		t.Errorf("body = %q, want {\"ok\":true}\\n", body)
	}
}

func TestPostCrawl_ValidRunsToCompletion(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://example.com/", htmlResponse(`<html><body>
		<a href="/about">About</a>
		<a href="/missing">Missing</a>
	</body></html>`))
	f.set("https://example.com/about", htmlResponse(`<html><body>ok</body></html>`))
	f.set("https://example.com/missing", statusResponse(404))

	_, h := newTestServer(t, f)

	reqBody := mustJSON(t, map[string]any{
		"seed":          "https://example.com/",
		"delay_ms":      0,
		"concurrency":   1,
		"max_pages":     10,
		"max_depth":     5,
		"ignore_robots": true,
		"use_sitemaps":  false,
	})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, body = %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	crawl := decodeJSON[store.Crawl](t, w)
	if crawl.Status != "running" {
		t.Errorf("initial status = %q, want running", crawl.Status)
	}
	if crawl.Seed != "https://example.com/" {
		t.Errorf("seed = %q", crawl.Seed)
	}

	detail := waitUntilDone(t, h, crawl.ID, 5*time.Second)
	if detail.Crawl.Status != "done" {
		t.Fatalf("final status = %q, want done (error=%q)", detail.Crawl.Status, detail.Crawl.Error)
	}
	if detail.Summary.Total != 3 {
		t.Errorf("summary.total = %d, want 3", detail.Summary.Total)
	}
	if detail.Summary.Status2xx != 2 {
		t.Errorf("summary.status_2xx = %d, want 2", detail.Summary.Status2xx)
	}
	if detail.Summary.Status4xx != 1 {
		t.Errorf("summary.status_4xx = %d, want 1", detail.Summary.Status4xx)
	}
}

func TestPostCrawl_Defaults(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://defaults.example/", statusResponse(200))
	_, h := newTestServer(t, f)

	reqBody := mustJSON(t, map[string]any{"seed": "https://defaults.example/"})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, body = %s", w.Code, w.Body.String())
	}
	crawl := decodeJSON[store.Crawl](t, w)

	var cfg map[string]any
	if err := json.Unmarshal(crawl.Config, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	want := map[string]any{
		"max_pages":          float64(500),
		"max_depth":          float64(10),
		"concurrency":        float64(4),
		"delay_ms":           float64(500),
		"timeout_ms":         float64(15000),
		"max_body_bytes":     float64(2097152),
		"user_agent":         "EANBot/0.1 (+https://xavi.net)",
		"include_subdomains": false,
		"ignore_robots":      false,
		"use_sitemaps":       true,
	}
	for k, v := range want {
		if cfg[k] != v {
			t.Errorf("config[%q] = %v, want %v", k, cfg[k], v)
		}
	}

	waitUntilDone(t, h, crawl.ID, 5*time.Second)
}

func TestPostCrawl_ExplicitZeroDelay(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://zerodelay.example/", statusResponse(200))
	_, h := newTestServer(t, f)

	reqBody := mustJSON(t, map[string]any{"seed": "https://zerodelay.example/", "delay_ms": 0})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	crawl := decodeJSON[store.Crawl](t, w)

	var cfg map[string]any
	json.Unmarshal(crawl.Config, &cfg)
	if cfg["delay_ms"] != float64(0) {
		t.Errorf("delay_ms = %v, want 0 (explicit zero must not become the default)", cfg["delay_ms"])
	}

	waitUntilDone(t, h, crawl.ID, 5*time.Second)
}

func TestPostCrawl_HeadersPassedToFetcherAndStored(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://headers.example/", statusResponse(200))

	st := newTestStore(t)
	var gotCfg crawler.Config
	factory := func(cfg crawler.Config) crawler.Fetcher {
		gotCfg = cfg
		return f
	}
	srv := New(st, Options{NewFetcher: factory})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})
	h := srv.Handler()

	reqBody := mustJSON(t, map[string]any{
		"seed":    "https://headers.example/",
		"headers": map[string]string{"X-Ean-Client": "abc"},
	})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, body = %s", w.Code, w.Body.String())
	}
	crawl := decodeJSON[store.Crawl](t, w)

	waitUntilDone(t, h, crawl.ID, 5*time.Second)

	if gotCfg.Headers["X-Ean-Client"] != "abc" {
		t.Errorf("NewFetcher received Config.Headers = %v, want X-Ean-Client=abc", gotCfg.Headers)
	}

	var cfg map[string]any
	if err := json.Unmarshal(crawl.Config, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	headers, ok := cfg["headers"].(map[string]any)
	if !ok {
		t.Fatalf("config[\"headers\"] = %v (%T), want an object", cfg["headers"], cfg["headers"])
	}
	if headers["X-Ean-Client"] != "abc" {
		t.Errorf("stored headers = %v, want X-Ean-Client=abc (name as sent by the user)", headers)
	}
}

func TestPostCrawl_HeadersAbsentStoredAsEmptyObject(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://noheaders.example/", statusResponse(200))
	_, h := newTestServer(t, f)

	reqBody := mustJSON(t, map[string]any{"seed": "https://noheaders.example/"})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	crawl := decodeJSON[store.Crawl](t, w)
	waitUntilDone(t, h, crawl.ID, 5*time.Second)

	var cfg map[string]any
	if err := json.Unmarshal(crawl.Config, &cfg); err != nil {
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

func TestPostCrawl_InvalidHeaderName(t *testing.T) {
	_, h := newTestServer(t, newFakeFetcher())

	reqBody := mustJSON(t, map[string]any{
		"seed":    "https://example.com/",
		"headers": map[string]string{"bad header": "abc"},
	})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	body := decodeJSON[errorsBody](t, w)
	want := `cabecera no válida: "bad header"`
	if len(body.Errors) != 1 || body.Errors[0] != want {
		t.Errorf("errors = %v, want [%q]", body.Errors, want)
	}
}

func TestPostCrawl_OriginAndInsecureTLSPassedToFetcherAndStored(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://origin.example/", statusResponse(200))

	st := newTestStore(t)
	var gotCfg crawler.Config
	factory := func(cfg crawler.Config) crawler.Fetcher {
		gotCfg = cfg
		return f
	}
	srv := New(st, Options{NewFetcher: factory})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})
	h := srv.Handler()

	reqBody := mustJSON(t, map[string]any{
		"seed":         "https://origin.example/",
		"origin":       "172.16.0.10",
		"insecure_tls": true,
	})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, body = %s", w.Code, w.Body.String())
	}
	crawl := decodeJSON[store.Crawl](t, w)

	waitUntilDone(t, h, crawl.ID, 5*time.Second)

	if gotCfg.Origin != "172.16.0.10" {
		t.Errorf("NewFetcher received Config.Origin = %q, want %q", gotCfg.Origin, "172.16.0.10")
	}
	if !gotCfg.InsecureTLS {
		t.Error("NewFetcher received Config.InsecureTLS = false, want true")
	}

	var cfg map[string]any
	if err := json.Unmarshal(crawl.Config, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if cfg["origin"] != "172.16.0.10" {
		t.Errorf("stored origin = %v, want %q", cfg["origin"], "172.16.0.10")
	}
	if cfg["insecure_tls"] != true {
		t.Errorf("stored insecure_tls = %v, want true", cfg["insecure_tls"])
	}
}

func TestPostCrawl_OriginAndInsecureTLSAbsentStoredAsZeroValues(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://noorigin.example/", statusResponse(200))
	_, h := newTestServer(t, f)

	reqBody := mustJSON(t, map[string]any{"seed": "https://noorigin.example/"})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	crawl := decodeJSON[store.Crawl](t, w)
	waitUntilDone(t, h, crawl.ID, 5*time.Second)

	var cfg map[string]any
	if err := json.Unmarshal(crawl.Config, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if cfg["origin"] != "" {
		t.Errorf("stored origin = %v, want \"\"", cfg["origin"])
	}
	if cfg["insecure_tls"] != false {
		t.Errorf("stored insecure_tls = %v, want false", cfg["insecure_tls"])
	}
}

func TestPostCrawl_InvalidOrigin(t *testing.T) {
	_, h := newTestServer(t, newFakeFetcher())

	reqBody := mustJSON(t, map[string]any{
		"seed":   "https://example.com/",
		"origin": "abc",
	})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	body := decodeJSON[errorsBody](t, w)
	want := `origin no válido: "abc"`
	if len(body.Errors) != 1 || body.Errors[0] != want {
		t.Errorf("errors = %v, want [%q]", body.Errors, want)
	}
}

// TestDefaultNewFetcherAppliesOrigin exercises the real (non-injected)
// NewFetcher built by New when Options.NewFetcher is nil: it must build an
// HTTPFetcher whose Origin/InsecureTLS come from the crawl's Config and
// whose OriginHost is the seed's normalized host. The seed uses a hostname
// that cannot resolve on its own ("sitio.test"); the crawl only succeeds if
// the default factory wires Origin/OriginHost correctly so the request is
// redirected to the httptest.Server's real address.
func TestDefaultNewFetcherAppliesOrigin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>ok</body></html>")
	}))
	defer srv.Close()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort error: %v", err)
	}

	st := newTestStore(t)
	s := New(st, Options{})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})
	h := s.Handler()

	reqBody := mustJSON(t, map[string]any{
		"seed":          "http://sitio.test:" + port + "/",
		"origin":        "127.0.0.1:" + port,
		"ignore_robots": true,
		"max_pages":     1,
	})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, body = %s", w.Code, w.Body.String())
	}
	crawl := decodeJSON[store.Crawl](t, w)

	detail := waitUntilDone(t, h, crawl.ID, 5*time.Second)
	if detail.Summary.Status2xx != 1 {
		t.Errorf("Summary.Status2xx = %d, want 1 (default fetcher did not honor Origin)", detail.Summary.Status2xx)
	}
}

func TestPostCrawl_InvalidReturnsAllErrors(t *testing.T) {
	_, h := newTestServer(t, newFakeFetcher())

	reqBody := mustJSON(t, map[string]any{
		"seed":        "",
		"max_pages":   -1,
		"max_depth":   -1,
		"concurrency": -1,
		"delay_ms":    -5,
	})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	body := decodeJSON[errorsBody](t, w)

	wantSubstrings := []string{
		"la semilla es obligatoria",
		"max_pages debe ser mayor que 0",
		"max_depth no puede ser negativo",
		"concurrency debe ser mayor que 0",
		"delay_ms no puede ser negativo",
	}
	if len(body.Errors) != len(wantSubstrings) {
		t.Fatalf("errors = %v, want %d entries", body.Errors, len(wantSubstrings))
	}
	for _, want := range wantSubstrings {
		found := false
		for _, got := range body.Errors {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("errors %v missing %q", body.Errors, want)
		}
	}
}

func TestPostCrawl_MalformedJSON(t *testing.T) {
	_, h := newTestServer(t, newFakeFetcher())

	w := doRequest(h, http.MethodPost, "/api/crawls", []byte(`{"seed":`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	body := decodeJSON[errorsBody](t, w)
	if len(body.Errors) != 1 || body.Errors[0] != "json malformado" {
		t.Errorf("errors = %v, want [json malformado]", body.Errors)
	}
}

func TestPostCrawl_BodyTooLarge(t *testing.T) {
	_, h := newTestServer(t, newFakeFetcher())

	padding := strings.Repeat("a", 2<<20) // 2 MiB, well over the 1 MiB limit
	big := mustJSON(t, map[string]any{"seed": "https://example.com/", "user_agent": padding})

	w := doRequest(h, http.MethodPost, "/api/crawls", big)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	body := decodeJSON[errorsBody](t, w)
	if len(body.Errors) != 1 || body.Errors[0] != "json malformado" {
		t.Errorf("errors = %v, want [json malformado]", body.Errors)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	_, h := newTestServer(t, newFakeFetcher())

	tests := []struct {
		method, path string
		wantAllow    string
	}{
		{http.MethodDelete, "/api/crawls", "GET, POST"},
		{http.MethodPut, "/api/crawls/1", "DELETE, GET"},
		{http.MethodGet, "/api/crawls/1/cancel", "POST"},
	}
	for _, tt := range tests {
		w := doRequest(h, tt.method, tt.path, nil)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status = %d, want 405", tt.method, tt.path, w.Code)
		}
		if allow := w.Header().Get("Allow"); allow != tt.wantAllow {
			t.Errorf("%s %s: Allow = %q, want %q", tt.method, tt.path, allow, tt.wantAllow)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s %s: Content-Type = %q, want application/json", tt.method, tt.path, ct)
		}
	}
}

func TestNotFound(t *testing.T) {
	_, h := newTestServer(t, newFakeFetcher())

	tests := []struct {
		name, path string
	}{
		{"unknown numeric id", "/api/crawls/999999"},
		{"non-numeric id", "/api/crawls/abc"},
		{"unknown route", "/api/does-not-exist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := doRequest(h, http.MethodGet, tt.path, nil)
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", w.Code)
			}
			body := decodeJSON[errorsBody](t, w)
			if len(body.Errors) != 1 || body.Errors[0] != "no encontrado" {
				t.Errorf("errors = %v, want [no encontrado]", body.Errors)
			}
		})
	}
}

func TestListCrawls(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://one.example/", statusResponse(200))
	f.set("https://two.example/", statusResponse(200))
	_, h := newTestServer(t, f)

	body1 := mustJSON(t, map[string]any{"seed": "https://one.example/", "ignore_robots": true})
	w1 := doRequest(h, http.MethodPost, "/api/crawls", body1)
	crawl1 := decodeJSON[store.Crawl](t, w1)
	waitUntilDone(t, h, crawl1.ID, 5*time.Second)

	body2 := mustJSON(t, map[string]any{"seed": "https://two.example/", "ignore_robots": true})
	w2 := doRequest(h, http.MethodPost, "/api/crawls", body2)
	crawl2 := decodeJSON[store.Crawl](t, w2)
	waitUntilDone(t, h, crawl2.ID, 5*time.Second)

	w := doRequest(h, http.MethodGet, "/api/crawls", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	listBody := decodeJSON[struct {
		Crawls []store.Crawl `json:"crawls"`
	}](t, w)
	if len(listBody.Crawls) != 2 {
		t.Fatalf("len(crawls) = %d, want 2", len(listBody.Crawls))
	}
	if listBody.Crawls[0].ID != crawl2.ID {
		t.Errorf("crawls[0].ID = %d, want %d (most recent first)", listBody.Crawls[0].ID, crawl2.ID)
	}
}

func setupFilterCrawl(t *testing.T) (http.Handler, int64) {
	t.Helper()
	f := newFakeFetcher()
	f.set("https://filter.example/", htmlResponse(`<html><body>
		<a href="/a">A</a>
		<a href="/b">B</a>
		<a href="/c">C</a>
	</body></html>`))
	f.set("https://filter.example/a", htmlResponse(`<html><head><title>Alpha Page</title></head><body>a</body></html>`))
	f.set("https://filter.example/b", statusResponse(404))
	f.set("https://filter.example/c", htmlResponse(`<html><head><title>Zebra Charlie</title></head><body>c</body></html>`))

	_, h := newTestServer(t, f)
	reqBody := mustJSON(t, map[string]any{
		"seed":          "https://filter.example/",
		"delay_ms":      0,
		"concurrency":   1,
		"max_pages":     10,
		"max_depth":     5,
		"ignore_robots": true,
		"use_sitemaps":  false,
	})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	crawl := decodeJSON[store.Crawl](t, w)
	waitUntilDone(t, h, crawl.ID, 5*time.Second)
	return h, crawl.ID
}

type pagesBody struct {
	Pages  []store.Page `json:"pages"`
	Total  int          `json:"total"`
	Limit  int          `json:"limit"`
	Offset int          `json:"offset"`
}

func TestListPages_FiltersAndPagination(t *testing.T) {
	h, id := setupFilterCrawl(t)

	t.Run("no filter", func(t *testing.T) {
		w := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d/pages", id), nil)
		body := decodeJSON[pagesBody](t, w)
		if body.Total != 4 {
			t.Errorf("total = %d, want 4", body.Total)
		}
	})

	t.Run("status 2xx", func(t *testing.T) {
		w := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d/pages?status=2xx", id), nil)
		body := decodeJSON[pagesBody](t, w)
		if body.Total != 3 {
			t.Errorf("total = %d, want 3", body.Total)
		}
	})

	t.Run("status 4xx", func(t *testing.T) {
		w := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d/pages?status=4xx", id), nil)
		body := decodeJSON[pagesBody](t, w)
		if body.Total != 1 {
			t.Errorf("total = %d, want 1", body.Total)
		}
		if len(body.Pages) != 1 || !strings.HasSuffix(body.Pages[0].URL, "/b") {
			t.Errorf("pages = %+v, want just /b", body.Pages)
		}
	})

	t.Run("invalid status", func(t *testing.T) {
		w := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d/pages?status=bogus", id), nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
		body := decodeJSON[errorsBody](t, w)
		if len(body.Errors) != 1 || body.Errors[0] != "status no válido" {
			t.Errorf("errors = %v, want [status no válido]", body.Errors)
		}
	})

	t.Run("query", func(t *testing.T) {
		w := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d/pages?q=Zebra", id), nil)
		body := decodeJSON[pagesBody](t, w)
		if body.Total != 1 || len(body.Pages) != 1 || !strings.HasSuffix(body.Pages[0].URL, "/c") {
			t.Errorf("pages = %+v, want just /c", body.Pages)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		w := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d/pages?limit=2&offset=0", id), nil)
		body := decodeJSON[pagesBody](t, w)
		if len(body.Pages) != 2 || body.Limit != 2 || body.Offset != 0 || body.Total != 4 {
			t.Errorf("page 1 = %+v", body)
		}

		w2 := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d/pages?limit=2&offset=2", id), nil)
		body2 := decodeJSON[pagesBody](t, w2)
		if len(body2.Pages) != 2 || body2.Offset != 2 {
			t.Errorf("page 2 = %+v", body2)
		}

		var allIDs []int64
		for _, p := range append(body.Pages, body2.Pages...) {
			allIDs = append(allIDs, p.ID)
		}
		sort.Slice(allIDs, func(i, j int) bool { return allIDs[i] < allIDs[j] })
		for i := 1; i < len(allIDs); i++ {
			if allIDs[i] == allIDs[i-1] {
				t.Errorf("duplicate id %d across pages", allIDs[i])
			}
		}
	})
}

type pageDetailBody struct {
	Page          store.Page   `json:"page"`
	Outlinks      []store.Link `json:"outlinks"`
	Inlinks       []store.Link `json:"inlinks"`
	OutlinksTotal int          `json:"outlinks_total"`
	InlinksTotal  int          `json:"inlinks_total"`
}

func TestGetPage_InOutLinks(t *testing.T) {
	h, id := setupFilterCrawl(t)

	w := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d/pages", id), nil)
	list := decodeJSON[pagesBody](t, w)

	var pageA store.Page
	for _, p := range list.Pages {
		if strings.HasSuffix(p.URL, "/a") {
			pageA = p
		}
	}
	if pageA.ID == 0 {
		t.Fatalf("page /a not found in %+v", list.Pages)
	}

	w2 := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d/pages/%d", id, pageA.ID), nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w2.Code, w2.Body.String())
	}
	detail := decodeJSON[pageDetailBody](t, w2)
	if len(detail.Outlinks) != 0 {
		t.Errorf("outlinks = %+v, want none", detail.Outlinks)
	}
	if detail.OutlinksTotal != 0 {
		t.Errorf("outlinks_total = %d, want 0", detail.OutlinksTotal)
	}
	if len(detail.Inlinks) != 1 {
		t.Fatalf("inlinks = %+v, want 1", detail.Inlinks)
	}
	if detail.InlinksTotal != 1 {
		t.Errorf("inlinks_total = %d, want 1", detail.InlinksTotal)
	}
	if detail.Inlinks[0].Text != "A" {
		t.Errorf("inlinks[0].Text = %q, want A", detail.Inlinks[0].Text)
	}
	if !strings.HasSuffix(detail.Inlinks[0].FromURL, "filter.example/") {
		t.Errorf("inlinks[0].FromURL = %q", detail.Inlinks[0].FromURL)
	}

	// Non-existent page id under a real crawl -> 404.
	w3 := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d/pages/999999", id), nil)
	if w3.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w3.Code)
	}
}

type brokenBody struct {
	Broken []store.BrokenPage `json:"broken"`
	Total  int                `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

func TestBrokenLinks(t *testing.T) {
	h, id := setupFilterCrawl(t)

	w := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d/broken", id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	body := decodeJSON[brokenBody](t, w)
	if body.Total != 1 || body.Limit != 100 || body.Offset != 0 {
		t.Errorf("total/limit/offset = %d/%d/%d, want 1/100/0", body.Total, body.Limit, body.Offset)
	}
	if len(body.Broken) != 1 {
		t.Fatalf("broken = %+v, want 1 entry", body.Broken)
	}
	if !strings.HasSuffix(body.Broken[0].Page.URL, "/b") {
		t.Errorf("broken page = %q, want suffix /b", body.Broken[0].Page.URL)
	}
	if body.Broken[0].ReferrersCount != 1 {
		t.Errorf("referrers_count = %d, want 1", body.Broken[0].ReferrersCount)
	}
}

func TestBrokenLinks_Pagination(t *testing.T) {
	h, id := setupFilterCrawl(t)

	w := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d/broken?limit=1&offset=0", id), nil)
	body := decodeJSON[brokenBody](t, w)
	if body.Limit != 1 || body.Offset != 0 || body.Total != 1 {
		t.Errorf("limit/offset/total = %d/%d/%d, want 1/0/1", body.Limit, body.Offset, body.Total)
	}
	if len(body.Broken) != 1 {
		t.Errorf("broken = %+v, want 1 entry", body.Broken)
	}

	w2 := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d/broken?offset=1", id), nil)
	body2 := decodeJSON[brokenBody](t, w2)
	if len(body2.Broken) != 0 {
		t.Errorf("broken past total = %+v, want none", body2.Broken)
	}
}

func TestCancelCrawl(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://cancel.example/", htmlResponse(`<a href="/slow">slow</a>`))
	f.setSlow("https://cancel.example/slow")
	_, h := newTestServer(t, f)

	reqBody := mustJSON(t, map[string]any{
		"seed":          "https://cancel.example/",
		"delay_ms":      0,
		"concurrency":   1,
		"max_pages":     10,
		"ignore_robots": true,
		"use_sitemaps":  false,
	})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	crawl := decodeJSON[store.Crawl](t, w)

	wc := doRequest(h, http.MethodPost, fmt.Sprintf("/api/crawls/%d/cancel", crawl.ID), nil)
	if wc.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", wc.Code, wc.Body.String())
	}

	detail := waitUntilDone(t, h, crawl.ID, 5*time.Second)
	if detail.Crawl.Status != "cancelled" {
		t.Errorf("status = %q, want cancelled", detail.Crawl.Status)
	}
}

func TestCancel_NotRunningStillReturns200(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://done.example/", statusResponse(200))
	_, h := newTestServer(t, f)

	reqBody := mustJSON(t, map[string]any{"seed": "https://done.example/", "ignore_robots": true})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	crawl := decodeJSON[store.Crawl](t, w)
	waitUntilDone(t, h, crawl.ID, 5*time.Second)

	wc := doRequest(h, http.MethodPost, fmt.Sprintf("/api/crawls/%d/cancel", crawl.ID), nil)
	if wc.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want 200", wc.Code)
	}

	wUnknown := doRequest(h, http.MethodPost, "/api/crawls/999999/cancel", nil)
	if wUnknown.Code != http.StatusNotFound {
		t.Errorf("cancel unknown id status = %d, want 404", wUnknown.Code)
	}
}

func TestDeleteCrawl(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://delete.example/", statusResponse(200))
	_, h := newTestServer(t, f)

	reqBody := mustJSON(t, map[string]any{"seed": "https://delete.example/", "ignore_robots": true})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	crawl := decodeJSON[store.Crawl](t, w)
	waitUntilDone(t, h, crawl.ID, 5*time.Second)

	wd := doRequest(h, http.MethodDelete, fmt.Sprintf("/api/crawls/%d", crawl.ID), nil)
	if wd.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", wd.Code)
	}

	wg := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d", crawl.ID), nil)
	if wg.Code != http.StatusNotFound {
		t.Errorf("get after delete status = %d, want 404", wg.Code)
	}
}

func TestDeleteCrawl_CancelsRunning(t *testing.T) {
	f := newFakeFetcher()
	f.set("https://deleterunning.example/", htmlResponse(`<a href="/slow">slow</a>`))
	f.setSlow("https://deleterunning.example/slow")
	_, h := newTestServer(t, f)

	reqBody := mustJSON(t, map[string]any{
		"seed":          "https://deleterunning.example/",
		"delay_ms":      0,
		"concurrency":   1,
		"ignore_robots": true,
		"use_sitemaps":  false,
	})
	w := doRequest(h, http.MethodPost, "/api/crawls", reqBody)
	crawl := decodeJSON[store.Crawl](t, w)

	wd := doRequest(h, http.MethodDelete, fmt.Sprintf("/api/crawls/%d", crawl.ID), nil)
	if wd.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", wd.Code)
	}

	wg := doRequest(h, http.MethodGet, fmt.Sprintf("/api/crawls/%d", crawl.ID), nil)
	if wg.Code != http.StatusNotFound {
		t.Errorf("get after delete status = %d, want 404", wg.Code)
	}
}
