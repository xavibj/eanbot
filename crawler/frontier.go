package crawler

// Frontier is a strict FIFO queue of URLs to crawl (breadth-first search),
// deduplicated by exact normalized URL. It is not safe for concurrent use;
// callers that share a Frontier across goroutines must protect it with
// their own mutex.
type Frontier struct {
	queue []frontierItem
	seen  map[string]bool
}

type frontierItem struct {
	url   string
	depth int
}

// NewFrontier returns an empty Frontier.
func NewFrontier() *Frontier {
	return &Frontier{seen: make(map[string]bool)}
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

// Pop removes and returns the oldest queued URL. ok is false if the
// frontier is empty.
func (f *Frontier) Pop() (url string, depth int, ok bool) {
	if len(f.queue) == 0 {
		return "", 0, false
	}
	item := f.queue[0]
	f.queue = f.queue[1:]
	return item.url, item.depth, true
}

// Len returns the number of URLs currently queued (not counting URLs
// already popped).
func (f *Frontier) Len() int {
	return len(f.queue)
}
