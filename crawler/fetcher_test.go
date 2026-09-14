package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPFetcherSendsUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	fetcher := NewHTTPFetcher("EANBot-Test/1.0", 5*time.Second, 1024)
	resp, err := fetcher.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if gotUA != "EANBot-Test/1.0" {
		t.Errorf("User-Agent sent = %q, want %q", gotUA, "EANBot-Test/1.0")
	}
	if resp.Status != 200 {
		t.Errorf("Status = %d, want 200", resp.Status)
	}
	if string(resp.Body) != "hello" {
		t.Errorf("Body = %q, want %q", resp.Body, "hello")
	}
}

func TestHTTPFetcherDoesNotFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			t.Error("fetcher should not have followed the redirect to /target")
		}
		w.Header().Set("Location", "/target")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer srv.Close()

	fetcher := NewHTTPFetcher("EANBot-Test/1.0", 5*time.Second, 1024)
	resp, err := fetcher.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if resp.Status != http.StatusMovedPermanently {
		t.Errorf("Status = %d, want %d", resp.Status, http.StatusMovedPermanently)
	}
	if resp.Location != "/target" {
		t.Errorf("Location = %q, want %q", resp.Location, "/target")
	}
}

func TestHTTPFetcherTruncatesBody(t *testing.T) {
	const maxBody = 10
	body := strings.Repeat("x", 100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	fetcher := NewHTTPFetcher("EANBot-Test/1.0", 5*time.Second, maxBody)
	resp, err := fetcher.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if !resp.Truncated {
		t.Error("expected Truncated = true")
	}
	if int64(len(resp.Body)) != maxBody {
		t.Errorf("len(Body) = %d, want %d", len(resp.Body), maxBody)
	}
	if resp.Size != maxBody {
		t.Errorf("Size = %d, want %d", resp.Size, maxBody)
	}
}

func TestHTTPFetcherNotTruncatedWhenUnderLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("short"))
	}))
	defer srv.Close()

	fetcher := NewHTTPFetcher("EANBot-Test/1.0", 5*time.Second, 1024)
	resp, err := fetcher.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch error: %v", err)
	}
	if resp.Truncated {
		t.Error("expected Truncated = false")
	}
}

func TestHTTPFetcherTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte("too late"))
	}))
	defer srv.Close()

	fetcher := NewHTTPFetcher("EANBot-Test/1.0", 10*time.Millisecond, 1024)
	_, err := fetcher.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
}
