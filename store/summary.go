package store

import (
	"fmt"
	"strings"
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

	row := s.r.QueryRow(
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
	if err := s.r.QueryRow(
		`SELECT COALESCE(SUM(duration_ms), 0), COUNT(*) FROM pages WHERE crawl_id = ? AND status > 0`,
		crawlID,
	).Scan(&durSum, &durCount); err != nil {
		return nil, fmt.Errorf("store: summarize crawl %d: %w", crawlID, err)
	}
	if durCount > 0 {
		sum.AvgDurationMs = durSum / durCount
	}

	rows, err := s.r.Query(
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

// brokenLinksWhere is the filter shared by BrokenLinks' count and select
// queries: pages that are broken (status >= 400, or status == 0 with a
// fetch error rather than a robots/scope block).
const brokenLinksWhere = "crawl_id = ? AND (status >= 400 OR (status = 0 AND blocked = 0))"

// BrokenLinks returns one page of the broken pages of a crawl (status >=
// 400, or status == 0 with a fetch error rather than a robots/scope block),
// ordered by URL, along with the total number of matches (ignoring
// limit/offset). Each result carries ReferrersCount, the number of links in
// the crawl pointing at it, computed by a correlated subquery on
// links_crawl_to rather than a separate query per page -- this is the whole
// point: a crawl with tens of thousands of broken pages must not turn into
// tens of thousands of queries (see "Rendimiento" in specs/003-store.md).
// The actual referring links are fetched separately via Referrers (or seen
// as a page's Inlinks via GetPage). It returns ErrNotFound if the crawl
// does not exist. limit defaults to 100 and is capped at 1000.
func (s *Store) BrokenLinks(crawlID int64, limit, offset int) ([]BrokenPage, int, error) {
	exists, err := s.crawlExists(crawlID)
	if err != nil {
		return nil, 0, err
	}
	if !exists {
		return nil, 0, ErrNotFound
	}

	limit = clampLimit(limit)
	if offset < 0 {
		offset = 0
	}

	var total int
	if err := s.r.QueryRow("SELECT COUNT(*) FROM pages WHERE "+brokenLinksWhere, crawlID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count broken links for crawl %d: %w", crawlID, err)
	}

	rows, err := s.r.Query(
		`SELECT id, crawl_id, url, depth, status, content_type, size, duration_ms,
			title, description, canonical, meta_robots, noindex, nofollow,
			h1, redirect_to, error, blocked, fetched_at,
			(SELECT COUNT(*) FROM links l WHERE l.crawl_id = p.crawl_id AND l.to_url = p.url) AS referrers_count
		 FROM pages p
		 WHERE `+brokenLinksWhere+`
		 ORDER BY url
		 LIMIT ? OFFSET ?`,
		crawlID, limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("store: broken links for crawl %d: %w", crawlID, err)
	}
	defer rows.Close()

	out := make([]BrokenPage, 0, limit)
	for rows.Next() {
		var (
			p          Page
			noindex    int
			nofollow   int
			blocked    int
			fetchedAtS string
			refCount   int
		)
		if err := rows.Scan(
			&p.ID, &p.CrawlID, &p.URL, &p.Depth, &p.Status, &p.ContentType, &p.Size, &p.DurationMs,
			&p.Title, &p.Description, &p.Canonical, &p.MetaRobots, &noindex, &nofollow,
			&p.H1, &p.RedirectTo, &p.Error, &blocked, &fetchedAtS, &refCount,
		); err != nil {
			return nil, 0, fmt.Errorf("store: broken links for crawl %d: %w", crawlID, err)
		}
		p.NoIndex = noindex != 0
		p.NoFollow = nofollow != 0
		p.Blocked = blocked != 0
		t, err := parseTime(fetchedAtS)
		if err != nil {
			return nil, 0, fmt.Errorf("store: broken links for crawl %d: %w", crawlID, err)
		}
		p.FetchedAt = t
		out = append(out, BrokenPage{Page: p, ReferrersCount: refCount})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("store: broken links for crawl %d: %w", crawlID, err)
	}
	return out, total, nil
}

// referrersBatchSize is the maximum number of URLs sent in a single
// Referrers query's IN (...) clause, to stay well under SQLite's default
// limit of 999 bound parameters (SQLITE_LIMIT_VARIABLE_NUMBER).
const referrersBatchSize = 500

// Referrers returns, for each of toURLs, up to perURL links (from other
// pages of the crawl) pointing at it, ordered by id, with FromURL filled.
// It is a single query per batch of up to referrersBatchSize URLs, using
// ROW_NUMBER() OVER (PARTITION BY to_url ORDER BY id) to cap each URL's
// referrers without a query per URL. perURL <= 0 defaults to 3; an empty
// toURLs returns an empty map without querying.
func (s *Store) Referrers(crawlID int64, toURLs []string, perURL int) (map[string][]Link, error) {
	out := map[string][]Link{}
	if len(toURLs) == 0 {
		return out, nil
	}
	if perURL <= 0 {
		perURL = 3
	}

	for start := 0; start < len(toURLs); start += referrersBatchSize {
		end := start + referrersBatchSize
		if end > len(toURLs) {
			end = len(toURLs)
		}
		if err := s.referrersBatch(crawlID, toURLs[start:end], perURL, out); err != nil {
			return nil, fmt.Errorf("store: referrers for crawl %d: %w", crawlID, err)
		}
	}
	return out, nil
}

func (s *Store) referrersBatch(crawlID int64, urls []string, perURL int, out map[string][]Link) error {
	placeholders := make([]string, len(urls))
	args := make([]any, 0, len(urls)+2)
	args = append(args, crawlID)
	for i, u := range urls {
		placeholders[i] = "?"
		args = append(args, u)
	}
	args = append(args, perURL)

	query := `SELECT from_page_id, from_url, to_url, text, nofollow, in_scope FROM (
		SELECT l.from_page_id AS from_page_id, p.url AS from_url, l.to_url AS to_url, l.text AS text,
			l.nofollow AS nofollow, l.in_scope AS in_scope,
			ROW_NUMBER() OVER (PARTITION BY l.to_url ORDER BY l.id) AS rn
		FROM links l JOIN pages p ON p.id = l.from_page_id
		WHERE l.crawl_id = ? AND l.to_url IN (` + strings.Join(placeholders, ",") + `)
	) WHERE rn <= ? ORDER BY to_url, rn`

	rows, err := s.r.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			l        Link
			nofollow int
			inScope  int
		)
		if err := rows.Scan(&l.FromPageID, &l.FromURL, &l.ToURL, &l.Text, &nofollow, &inScope); err != nil {
			return err
		}
		l.NoFollow = nofollow != 0
		l.InScope = inScope != 0
		out[l.ToURL] = append(out[l.ToURL], l)
	}
	return rows.Err()
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

	rows, err := s.r.Query(
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
