package store

import (
	"encoding/json"
	"time"
)

// Crawl is a single crawl run.
type Crawl struct {
	ID         int64           `json:"id"`
	Seed       string          `json:"seed"`
	Status     string          `json:"status"`
	Config     json.RawMessage `json:"config"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at"`
	PagesCount int             `json:"pages_count"`
	Error      string          `json:"error"`
	RobotsTxt  string          `json:"robots_txt"`
}

// Page is a single fetched (or blocked/errored) URL within a crawl.
type Page struct {
	ID          int64     `json:"id"`
	CrawlID     int64     `json:"crawl_id"`
	URL         string    `json:"url"`
	Depth       int       `json:"depth"`
	Status      int       `json:"status"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	DurationMs  int64     `json:"duration_ms"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Canonical   string    `json:"canonical"`
	MetaRobots  string    `json:"meta_robots"`
	NoIndex     bool      `json:"noindex"`
	NoFollow    bool      `json:"nofollow"`
	H1          string    `json:"h1"`
	RedirectTo  string    `json:"redirect_to"`
	Error       string    `json:"error"`
	Blocked     bool      `json:"blocked"`
	FetchedAt   time.Time `json:"fetched_at"`
}

// Link is an outgoing link found on a page.
type Link struct {
	FromPageID int64  `json:"from_page_id"`
	FromURL    string `json:"from_url"`
	ToURL      string `json:"to_url"`
	Text       string `json:"text"`
	NoFollow   bool   `json:"nofollow"`
	InScope    bool   `json:"in_scope"`
}

// PageFilter selects a subset of a crawl's pages for ListPages.
type PageFilter struct {
	Status string
	Query  string
	Limit  int
	Offset int
}

// Summary aggregates a crawl's pages.
type Summary struct {
	Total         int            `json:"total"`
	Status2xx     int            `json:"status_2xx"`
	Status3xx     int            `json:"status_3xx"`
	Status4xx     int            `json:"status_4xx"`
	Status5xx     int            `json:"status_5xx"`
	Errors        int            `json:"errors"`
	Blocked       int            `json:"blocked"`
	NoIndex       int            `json:"noindex"`
	ContentTypes  map[string]int `json:"content_types"`
	MaxDepth      int            `json:"max_depth"`
	AvgDurationMs int64          `json:"avg_duration_ms"`
}

// LinkLimit caps the number of outlinks/inlinks GetPage returns, so a page
// with an unusually large number of links never turns a single request into
// an unbounded response.
const LinkLimit = 500

// PageDetail is a page plus its links, as returned by GetPage. Outlinks and
// Inlinks are capped at LinkLimit (ordered by id); OutlinksTotal and
// InlinksTotal report the true counts.
type PageDetail struct {
	Page          Page   `json:"page"`
	Outlinks      []Link `json:"outlinks"`
	Inlinks       []Link `json:"inlinks"`
	OutlinksTotal int    `json:"outlinks_total"`
	InlinksTotal  int    `json:"inlinks_total"`
}

// BrokenPage pairs a broken page with the number of links in the crawl that
// point to it. The links themselves are fetched separately (Referrers or
// GetPage's Inlinks) to keep BrokenLinks a single, cheap, paginated query.
type BrokenPage struct {
	Page           Page `json:"page"`
	ReferrersCount int  `json:"referrers_count"`
}
