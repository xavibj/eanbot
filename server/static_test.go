package server

import (
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func serveStaticTest(fsys fs.FS, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	serveStatic(fsys, w, req)
	return w
}

func TestServeStatic_IndexAndAssets(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":            {Data: []byte(`<div id="app"></div>`), ModTime: time.Now()},
		"assets/app.abc123.js":  {Data: []byte("console.log('hi')"), ModTime: time.Now()},
		"assets/app.abc123.css": {Data: []byte("body{color:red}"), ModTime: time.Now()},
		"favicon.ico":           {Data: []byte("ico"), ModTime: time.Now()},
	}

	t.Run("root serves index.html", func(t *testing.T) {
		w := serveStaticTest(fsys, "GET", "/")
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html prefix", ct)
		}
		if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("Cache-Control = %q, want no-cache", cc)
		}
		if !strings.Contains(w.Body.String(), `<div id="app">`) {
			t.Errorf("body = %q, want to contain <div id=\"app\">", w.Body.String())
		}
	})

	t.Run("existing js asset", func(t *testing.T) {
		w := serveStaticTest(fsys, "GET", "/assets/app.abc123.js")
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
			t.Errorf("Content-Type = %q, want text/javascript prefix", ct)
		}
		if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
			t.Errorf("Cache-Control = %q, want immutable", cc)
		}
	})

	t.Run("existing css asset", func(t *testing.T) {
		w := serveStaticTest(fsys, "GET", "/assets/app.abc123.css")
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
			t.Errorf("Content-Type = %q, want text/css prefix", ct)
		}
		if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
			t.Errorf("Cache-Control = %q, want immutable", cc)
		}
	})

	t.Run("existing non-asset file keeps no-cache", func(t *testing.T) {
		w := serveStaticTest(fsys, "GET", "/favicon.ico")
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("Cache-Control = %q, want no-cache", cc)
		}
	})

	t.Run("unknown path falls back to index.html", func(t *testing.T) {
		w := serveStaticTest(fsys, "GET", "/crawls/42")
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html prefix", ct)
		}
		if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("Cache-Control = %q, want no-cache", cc)
		}
		if !strings.Contains(w.Body.String(), `<div id="app">`) {
			t.Errorf("body = %q, want the SPA shell", w.Body.String())
		}
	})
}

// TestStaticFS_Embedded exercises the real embedded server/static
// directory (the placeholder committed for this phase) through the full
// Handler(), so a regression in the //go:embed wiring itself is caught.
func TestStaticFS_Embedded(t *testing.T) {
	st := newTestStore(t)
	srv := New(st, Options{NewFetcher: newFakeFetcherFactory(newFakeFetcher())})
	h := srv.Handler()

	w := doRequest(h, "GET", "/", nil)
	if w.Code != 200 {
		t.Fatalf("GET / status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html prefix", ct)
	}
	if !strings.Contains(w.Body.String(), `<div id="app"`) {
		t.Errorf("body does not contain <div id=\"app\": %q", w.Body.String())
	}

	w2 := doRequest(h, "GET", "/some/unknown/route", nil)
	if w2.Code != 200 {
		t.Fatalf("GET /some/unknown/route status = %d, want 200", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), `<div id="app"`) {
		t.Errorf("SPA fallback body does not contain <div id=\"app\": %q", w2.Body.String())
	}
}
