package crawler

// Frontier is a strict FIFO queue of URLs to crawl (breadth-first search),
// deduplicated by exact normalized URL. It is not safe for concurrent use;
// callers that share a Frontier across goroutines must protect it with
// their own mutex.
type Frontier struct {
	queue []frontierItem
	seen  map[string]bool

	// noFollow tracks, for URLs currently queued, whether they were
	// discovered only through nofollow links (rel=nofollow anchors, or
	// links found on a page itself served with a nofollow directive). It
	// only ever holds entries for URLs still in queue; Pop removes the
	// entry (if any) for the URL it returns.
	noFollow map[string]bool
}

type frontierItem struct {
	url   string
	depth int
}

// NewFrontier returns an empty Frontier.
func NewFrontier() *Frontier {
	return &Frontier{seen: make(map[string]bool), noFollow: make(map[string]bool)}
}

// Push enqueues url at depth. It returns false without modifying the
// frontier if url was already seen, whether it is still queued or has
// already been popped.
func (f *Frontier) Push(url string, depth int) bool {
	if f.seen[url] {
		return false
	}
	f.seen[url] = true
	f.queue = append(f.queue, frontierItem{url: url, depth: depth})
	return true
}

// PushNoFollow enqueues url at depth exactly like Push, additionally marking
// it as discovered via a nofollow link (or a nofollow page). It returns
// false, like Push, if url was already seen.
func (f *Frontier) PushNoFollow(url string, depth int) bool {
	if !f.Push(url, depth) {
		return false
	}
	f.noFollow[url] = true
	return true
}

// ClearNoFollow removes the "discovered via nofollow" mark from url, if it
// is still queued. It is a no-op if url was never marked or has already
// been popped.
func (f *Frontier) ClearNoFollow(url string) {
	delete(f.noFollow, url)
}

// Pop removes and returns the oldest queued URL, together with whether it
// was marked as discovered only via nofollow links. ok is false if the
// frontier is empty.
func (f *Frontier) Pop() (url string, depth int, viaNoFollow bool, ok bool) {
	if len(f.queue) == 0 {
		return "", 0, false, false
	}
	item := f.queue[0]
	f.queue = f.queue[1:]
	viaNoFollow = f.noFollow[item.url]
	delete(f.noFollow, item.url)
	return item.url, item.depth, viaNoFollow, true
}

// Len returns the number of URLs currently queued (not counting URLs
// already popped).
func (f *Frontier) Len() int {
	return len(f.queue)
}
