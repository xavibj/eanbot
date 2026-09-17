package store

import (
	"sync"
	"time"
)

// BatchWriter buffers pages for a single crawl and flushes them to AddPages
// in batches, rather than one transaction (and one WAL append) per page.
// See "Lotes" in specs/003-store.md: with a crawl of over a million pages,
// one commit per page made the WAL grow far faster than checkpoints could
// reclaim it. It is the recommended way for crawler.Sink implementations
// (server's and cmd/eanbot's) to persist pages.
//
// Add is safe for concurrent use, since crawler.Run drives several worker
// goroutines that each call Sink.Page.
type BatchWriter struct {
	st       *Store
	crawlID  int64
	maxItems int
	maxDelay time.Duration

	mu     sync.Mutex
	buf    []PageWithLinks
	timer  *time.Timer
	err    error
	closed bool
}

// NewBatchWriter returns a BatchWriter that flushes to crawlID's pages
// whenever maxItems have been queued, or maxDelay has passed since the
// first currently-pending item, whichever comes first.
func NewBatchWriter(s *Store, crawlID int64, maxItems int, maxDelay time.Duration) *BatchWriter {
	return &BatchWriter{st: s, crawlID: crawlID, maxItems: maxItems, maxDelay: maxDelay}
}

// Add queues a page (and its outgoing links) for the next flush. It never
// blocks the caller longer than a flush itself takes -- which happens
// in-line, in this call, only when the batch has just reached maxItems;
// otherwise Add only appends to the buffer and (for the first pending item)
// arms the maxDelay timer, both O(1) under the mutex.
func (b *BatchWriter) Add(p Page, links []Link) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.buf = append(b.buf, PageWithLinks{Page: p, Links: links})
	first := len(b.buf) == 1
	full := len(b.buf) >= b.maxItems
	if first {
		b.timer = time.AfterFunc(b.maxDelay, func() { b.flush() })
	}
	b.mu.Unlock()

	if full {
		b.flush()
	}
}

// Flush synchronously writes whatever is currently pending. It is a no-op
// (returns nil) if nothing is pending.
func (b *BatchWriter) Flush() error {
	return b.flush()
}

// flush is Add/Flush/Close's shared implementation: it takes ownership of
// the current buffer under the lock, then does the actual (unlocked, so
// concurrent Adds are not blocked by it) write.
func (b *BatchWriter) flush() error {
	b.mu.Lock()
	if len(b.buf) == 0 {
		b.mu.Unlock()
		return nil
	}
	batch := b.buf
	b.buf = nil
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.mu.Unlock()

	_, err := b.st.AddPages(b.crawlID, batch)
	if err != nil {
		b.mu.Lock()
		if b.err == nil {
			b.err = err
		}
		b.mu.Unlock()
	}
	return err
}

// Close flushes whatever is still pending and stops the maxDelay timer. It
// is idempotent: calling it more than once only flushes (and stops the
// timer) the first time. It returns the first flush error not yet reported
// by Flush or Close, whether that happened just now or from an earlier
// timer-triggered flush.
func (b *BatchWriter) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()

	ferr := b.flush()

	b.mu.Lock()
	defer b.mu.Unlock()
	if ferr != nil {
		return ferr
	}
	return b.err
}

// Err returns the last flush error seen so far (nil if every flush so far
// has succeeded).
func (b *BatchWriter) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}
