package store

import (
	"fmt"
)

// Summarize aggregates the pages of a crawl. It returns ErrNotFound if the
// crawl does not exist.
func (s *Store) Summarize(crawlID int64) (*Summary, error) {
	exists, err := s.crawlExists(crawlID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}

	sum := &Summary{ContentTypes: map[string]int{}}

	row := s.db.QueryRow(
		`SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status BETWEEN 200 AND 299),
			COUNT(*) FILTER (WHERE status BETWEEN 300 AND 399),
			COUNT(*) FILTER (WHERE status BETWEEN 400 AND 499),
			COUNT(*) FILTER (WHERE status BETWEEN 500 AND 599),
			COUNT(*) FILTER (WHERE status = 0 AND blocked = 0),
			COUNT(*) FILTER (WHERE blocked = 1),
			COUNT(*) FILTER (WHERE noindex = 1),
			COALESCE(MAX(depth), 0)
		 FROM pages WHERE crawl_id = ?`,
		crawlID,
	)
	if err := row.Scan(
		&sum.Total, &sum.Status2xx, &sum.Status3xx, &sum.Status4xx, &sum.Status5xx,
		&sum.Errors, &sum.Blocked, &sum.NoIndex, &sum.MaxDepth,
	); err != nil {
		return nil, fmt.Errorf("store: summarize crawl %d: %w", crawlID, err)
	}

	var durSum, durCount int64
	if err := s.db.QueryRow(
		`SELECT COALESCE(SUM(duration_ms), 0), COUNT(*) FROM pages WHERE crawl_id = ? AND status > 0`,
		crawlID,
	).Scan(&durSum, &durCount); err != nil {
		return nil, fmt.Errorf("store: summarize crawl %d: %w", crawlID, err)
	}
	if durCount > 0 {
		sum.AvgDurationMs = durSum / durCount
	}

	rows, err := s.db.Query(
		`SELECT content_type, COUNT(*) FROM pages WHERE crawl_id = ? AND content_type != '' GROUP BY content_type`,
		crawlID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: summarize crawl %d: %w", crawlID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var ct string
		var n int
		if err := rows.Scan(&ct, &n); err != nil {
			return nil, fmt.Errorf("store: summarize crawl %d: %w", crawlID, err)
		}
		sum.ContentTypes[ct] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: summarize crawl %d: %w", crawlID, err)
	}

	return sum, nil
}

// BrokenLinks returns the pages of a crawl that are broken (status >= 400,
// or status == 0 with a fetch error rather than a robots/scope block),
// ordered by URL, each with the links that pointed to it (ordered by the
// referring page's URL). It returns ErrNotFound if the crawl does not
// exist.
func (s *Store) BrokenLinks(crawlID int64) ([]BrokenLink, error) {
	exists, err := s.crawlExists(crawlID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}

	rows, err := s.db.Query(
		`SELECT id, crawl_id, url, depth, status, content_type, size, duration_ms,
			title, description, canonical, meta_robots, noindex, nofollow,
			h1, redirect_to, error, blocked, fetched_at
		 FROM pages
		 WHERE crawl_id = ? AND (status >= 400 OR (status = 0 AND blocked = 0))
		 ORDER BY url`,
		crawlID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: broken links for crawl %d: %w", crawlID, err)
	}
	defer rows.Close()

	var pages []Page
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, fmt.Errorf("store: broken links for crawl %d: %w", crawlID, err)
		}
		pages = append(pages, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: broken links for crawl %d: %w", crawlID, err)
	}

	out := make([]BrokenLink, 0, len(pages))
	for _, p := range pages {
		referrers, err := s.linksToURL(crawlID, p.URL)
		if err != nil {
			return nil, fmt.Errorf("store: broken links for crawl %d: %w", crawlID, err)
		}
		out = append(out, BrokenLink{Page: p, Referrers: referrers})
	}
	return out, nil
}

// Outlinks returns, for each distinct destination URL linked to within a
// crawl, the number of pages that link to it. It is not required by v1 but
// kept as a small convenience built on the same schema.
func (s *Store) Outlinks(crawlID int64) (map[string]int, error) {
	exists, err := s.crawlExists(crawlID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}

	rows, err := s.db.Query(
		`SELECT to_url, COUNT(*) FROM links WHERE crawl_id = ? GROUP BY to_url`,
		crawlID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: outlinks for crawl %d: %w", crawlID, err)
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var url string
		var n int
		if err := rows.Scan(&url, &n); err != nil {
			return nil, fmt.Errorf("store: outlinks for crawl %d: %w", crawlID, err)
		}
		out[url] = n
	}
	return out, rows.Err()
}
