package store

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestBatchWriter_FlushesByMaxItems(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()

	bw := NewBatchWriter(s, c.ID, 3, time.Hour) // maxDelay long enough that only maxItems can trigger this
	for i := 0; i < 3; i++ {
		bw.Add(Page{CrawlID: c.ID, URL: fmt.Sprintf("https://example.com/%d", i), Status: 200, FetchedAt: now}, nil)
	}

	got, err := s.GetCrawl(c.ID)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}
	if got.PagesCount != 3 {
		t.Fatalf("PagesCount = %d, want 3 (flush by maxItems)", got.PagesCount)
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestBatchWriter_FlushesByMaxDelay(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()

	bw := NewBatchWriter(s, c.ID, 100, 50*time.Millisecond)
	bw.Add(Page{CrawlID: c.ID, URL: "https://example.com/", Status: 200, FetchedAt: now}, nil)

	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := s.GetCrawl(c.ID)
		if err != nil {
			t.Fatalf("GetCrawl() error = %v", err)
		}
		if got.PagesCount == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("page was not flushed by maxDelay within 2s")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := bw.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestBatchWriter_FlushesOnClose(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()

	bw := NewBatchWriter(s, c.ID, 100, time.Hour)
	bw.Add(Page{CrawlID: c.ID, URL: "https://example.com/", Status: 200, FetchedAt: now}, nil)

	got, err := s.GetCrawl(c.ID)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}
	if got.PagesCount != 0 {
		t.Fatalf("PagesCount = %d before Close(), want 0 (nothing flushed yet)", got.PagesCount)
	}

	if err := bw.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	got, err = s.GetCrawl(c.ID)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}
	if got.PagesCount != 1 {
		t.Fatalf("PagesCount = %d after Close(), want 1", got.PagesCount)
	}

	// Close must be idempotent: a second call must not panic, re-flush or error.
	if err := bw.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	got, err = s.GetCrawl(c.ID)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}
	if got.PagesCount != 1 {
		t.Fatalf("PagesCount after second Close() = %d, want still 1", got.PagesCount)
	}
}

// TestBatchWriter_ExposesFailedFlushError adds a page for a crawl id that
// does not exist (the pages.crawl_id foreign key makes AddPages fail), then
// checks that both Flush and Err surface it, per specs/003-store.md.
func TestBatchWriter_ExposesFailedFlushError(t *testing.T) {
	s := openTestStore(t)
	const missingCrawlID = 999999

	bw := NewBatchWriter(s, missingCrawlID, 100, time.Hour)
	bw.Add(Page{CrawlID: missingCrawlID, URL: "https://example.com/", Status: 200, FetchedAt: time.Now().UTC()}, nil)

	if err := bw.Flush(); err == nil {
		t.Error("Flush() error = nil, want error for a page added to a non-existent crawl")
	}
	if bw.Err() == nil {
		t.Error("Err() = nil, want the failed flush's error")
	}
}

// TestBatchWriter_CloseReturnsError checks that Close reports a flush
// failure even when the failing flush happened via a Close call directly
// (nothing pending was flushed successfully before it).
func TestBatchWriter_CloseReturnsError(t *testing.T) {
	s := openTestStore(t)
	const missingCrawlID = 999999

	bw := NewBatchWriter(s, missingCrawlID, 100, time.Hour)
	bw.Add(Page{CrawlID: missingCrawlID, URL: "https://example.com/", Status: 200, FetchedAt: time.Now().UTC()}, nil)

	if err := bw.Close(); err == nil {
		t.Error("Close() error = nil, want error for a page added to a non-existent crawl")
	}
}

// TestBatchWriter_ConcurrentAdd exercises Add from several goroutines at
// once, as crawler.Run's several worker goroutines do, and checks every
// page lands exactly once.
func TestBatchWriter_ConcurrentAdd(t *testing.T) {
	s := openTestStore(t)
	c := mustCreateCrawl(t, s, "https://example.com")
	now := time.Now().UTC()

	const workers = 8
	const perWorker = 25
	bw := NewBatchWriter(s, c.ID, 10, 20*time.Millisecond)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				bw.Add(Page{
					CrawlID:   c.ID,
					URL:       fmt.Sprintf("https://example.com/%d/%d", w, i),
					Status:    200,
					FetchedAt: now,
				}, nil)
			}
		}(w)
	}
	wg.Wait()

	if err := bw.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	got, err := s.GetCrawl(c.ID)
	if err != nil {
		t.Fatalf("GetCrawl() error = %v", err)
	}
	want := workers * perWorker
	if got.PagesCount != want {
		t.Fatalf("PagesCount = %d, want %d", got.PagesCount, want)
	}
}
