package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AddPage inserts a page and its outgoing links. It is AddPages with a
// single-element batch; see AddPages for the transaction and counter
// semantics.
func (s *Store) AddPage(p Page, links []Link) (int64, error) {
	ids, err := s.AddPages(p.CrawlID, []PageWithLinks{{Page: p, Links: links}})
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

// AddPages inserts a batch of pages (and each one's outgoing links) in a
// single transaction, and updates the owning crawl's O(1) summary counters
// (pages_count, count_*, max_depth, duration_sum/duration_n) and
// crawl_content_types in that same transaction, per specs/003-store.md
// ("Contadores, lotes y checkpoints"). It returns the new page ids, in
// batch order.
//
// If any URL in the batch already exists in the crawl (the UNIQUE(crawl_id,
// url) constraint, whether against an already-stored page or against an
// earlier page in the same batch), the whole batch -- pages, links and
// counters alike -- is rolled back and an error is returned. This is what
// BatchWriter relies on: a batch either lands entirely or not at all, so a
// caller retrying it (or a future counters backfill) never has to reconcile
// a partial write.
func (s *Store) AddPages(crawlID int64, batch []PageWithLinks) ([]int64, error) {
	if len(batch) == 0 {
		return nil, nil
	}

	tx, err := s.w.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: add pages to crawl %d: %w", crawlID, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	pageStmt, err := tx.Prepare(
		`INSERT INTO pages (
			crawl_id, url, depth, status, content_type, size, duration_ms,
			title, description, canonical, meta_robots, noindex, nofollow,
			h1, redirect_to, error, blocked, fetched_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: add pages to crawl %d: %w", crawlID, err)
	}
	defer pageStmt.Close()

	linkStmt, err := tx.Prepare(
		`INSERT INTO links (crawl_id, from_page_id, to_url, text, nofollow, in_scope) VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: add pages to crawl %d: %w", crawlID, err)
	}
	defer linkStmt.Close()

	ids := make([]int64, 0, len(batch))
	var (
		c2xx, c3xx, c4xx, c5xx, cErr, cBlocked, cNoIndex, maxDepth int
		durSum, durN                                               int64
	)
	contentTypes := map[string]int{}

	for _, item := range batch {
		p := item.Page
		res, err := pageStmt.Exec(
			p.CrawlID, p.URL, p.Depth, p.Status, p.ContentType, p.Size, p.DurationMs,
			p.Title, p.Description, p.Canonical, p.MetaRobots, boolToInt(p.NoIndex), boolToInt(p.NoFollow),
			p.H1, p.RedirectTo, p.Error, boolToInt(p.Blocked), p.FetchedAt.UTC().Format(time.RFC3339Nano),
		)
		if err != nil {
			return nil, fmt.Errorf("store: add page %q to crawl %d: %w", p.URL, crawlID, err)
		}
		pageID, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("store: add page %q to crawl %d: %w", p.URL, crawlID, err)
		}
		ids = append(ids, pageID)

		for _, l := range item.Links {
			if _, err := linkStmt.Exec(
				p.CrawlID, pageID, l.ToURL, l.Text, boolToInt(l.NoFollow), boolToInt(l.InScope),
			); err != nil {
				return nil, fmt.Errorf("store: add link %q from page %q: %w", l.ToURL, p.URL, err)
			}
		}

		switch {
		case p.Status >= 200 && p.Status <= 299:
			c2xx++
		case p.Status >= 300 && p.Status <= 399:
			c3xx++
		case p.Status >= 400 && p.Status <= 499:
			c4xx++
		case p.Status >= 500 && p.Status <= 599:
			c5xx++
		case p.Status == 0 && !p.Blocked:
			cErr++
		}
		if p.Blocked {
			cBlocked++
		}
		if p.NoIndex {
			cNoIndex++
		}
		if p.Depth > maxDepth {
			maxDepth = p.Depth
		}
		if p.Status > 0 {
			durSum += p.DurationMs
			durN++
		}
		if p.ContentType != "" {
			contentTypes[p.ContentType]++
		}
	}

	if _, err := tx.Exec(
		`UPDATE crawls SET
			pages_count = pages_count + ?,
			count_2xx = count_2xx + ?,
			count_3xx = count_3xx + ?,
			count_4xx = count_4xx + ?,
			count_5xx = count_5xx + ?,
			count_errors = count_errors + ?,
			count_blocked = count_blocked + ?,
			count_noindex = count_noindex + ?,
			max_depth = MAX(max_depth, ?),
			duration_sum = duration_sum + ?,
			duration_n = duration_n + ?
		 WHERE id = ?`,
		len(batch), c2xx, c3xx, c4xx, c5xx, cErr, cBlocked, cNoIndex, maxDepth, durSum, durN, crawlID,
	); err != nil {
		return nil, fmt.Errorf("store: update counters for crawl %d: %w", crawlID, err)
	}

	for ct, n := range contentTypes {
		if _, err := tx.Exec(
			`INSERT INTO crawl_content_types (crawl_id, content_type, n) VALUES (?, ?, ?)
			 ON CONFLICT(crawl_id, content_type) DO UPDATE SET n = n + excluded.n`,
			crawlID, ct, n,
		); err != nil {
			return nil, fmt.Errorf("store: update content types for crawl %d: %w", crawlID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: add pages to crawl %d: %w", crawlID, err)
	}
	return ids, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// GetPage returns a page plus its outgoing links (Outlinks) and the links
// from other pages in the same crawl that point to it (Inlinks, with
// FromURL filled). Both lists are capped at LinkLimit; OutlinksTotal and
// InlinksTotal report the true counts so callers know when the list was
// truncated.
func (s *Store) GetPage(crawlID, pageID int64) (*PageDetail, error) {
	row := s.r.QueryRow(
		`SELECT id, crawl_id, url, depth, status, content_type, size, duration_ms,
			title, description, canonical, meta_robots, noindex, nofollow,
			h1, redirect_to, error, blocked, fetched_at
		 FROM pages WHERE crawl_id = ? AND id = ?`,
		crawlID, pageID,
	)
	p, err := scanPage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get page %d in crawl %d: %w", pageID, crawlID, err)
	}

	out, outTotal, err := s.linksFromPage(p.ID)
	if err != nil {
		return nil, fmt.Errorf("store: get page %d in crawl %d: %w", pageID, crawlID, err)
	}
	in, inTotal, err := s.linksToURL(crawlID, p.URL)
	if err != nil {
		return nil, fmt.Errorf("store: get page %d in crawl %d: %w", pageID, crawlID, err)
	}
	return &PageDetail{
		Page:          *p,
		Outlinks:      out,
		Inlinks:       in,
		OutlinksTotal: outTotal,
		InlinksTotal:  inTotal,
	}, nil
}

// linksFromPage returns up to LinkLimit outgoing links of a page, ordered by
// id, plus the true total (via links_from).
func (s *Store) linksFromPage(pageID int64) ([]Link, int, error) {
	var total int
	if err := s.r.QueryRow(`SELECT COUNT(*) FROM links WHERE from_page_id = ?`, pageID).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.r.Query(
		`SELECT from_page_id, to_url, text, nofollow, in_scope FROM links WHERE from_page_id = ? ORDER BY id LIMIT ?`,
		pageID, LinkLimit,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []Link
	for rows.Next() {
		var (
			l        Link
			nofollow int
			inScope  int
		)
		if err := rows.Scan(&l.FromPageID, &l.ToURL, &l.Text, &nofollow, &inScope); err != nil {
			return nil, 0, err
		}
		l.NoFollow = nofollow != 0
		l.InScope = inScope != 0
		out = append(out, l)
	}
	return out, total, rows.Err()
}

// linksToURL returns up to LinkLimit links (from other pages of the same
// crawl) pointing at url, ordered by id, with FromURL filled, plus the true
// total (via links_crawl_to).
func (s *Store) linksToURL(crawlID int64, url string) ([]Link, int, error) {
	var total int
	if err := s.r.QueryRow(`SELECT COUNT(*) FROM links WHERE crawl_id = ? AND to_url = ?`, crawlID, url).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.r.Query(
		`SELECT l.from_page_id, p.url, l.to_url, l.text, l.nofollow, l.in_scope
		 FROM links l JOIN pages p ON p.id = l.from_page_id
		 WHERE l.crawl_id = ? AND l.to_url = ?
		 ORDER BY l.id LIMIT ?`,
		crawlID, url, LinkLimit,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []Link
	for rows.Next() {
		var (
			l        Link
			nofollow int
			inScope  int
		)
		if err := rows.Scan(&l.FromPageID, &l.FromURL, &l.ToURL, &l.Text, &nofollow, &inScope); err != nil {
			return nil, 0, err
		}
		l.NoFollow = nofollow != 0
		l.InScope = inScope != 0
		out = append(out, l)
	}
	return out, total, rows.Err()
}

// FindPageByURL returns the page with the given URL in the given crawl.
func (s *Store) FindPageByURL(crawlID int64, url string) (*Page, error) {
	row := s.r.QueryRow(
		`SELECT id, crawl_id, url, depth, status, content_type, size, duration_ms,
			title, description, canonical, meta_robots, noindex, nofollow,
			h1, redirect_to, error, blocked, fetched_at
		 FROM pages WHERE crawl_id = ? AND url = ?`,
		crawlID, url,
	)
	p, err := scanPage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: find page %q in crawl %d: %w", url, crawlID, err)
	}
	return p, nil
}

// pageFilterCounterColumn maps a PageFilter.Status value to the crawls
// counter column that holds its O(1) total (see "Contadores" in
// specs/003-store.md). ListPages uses it instead of COUNT(*) whenever
// Query == "", since a plain COUNT(*) over pages does not scale to the
// millions of rows a long crawl accumulates.
var pageFilterCounterColumn = map[string]string{
	"":        "pages_count",
	"2xx":     "count_2xx",
	"3xx":     "count_3xx",
	"4xx":     "count_4xx",
	"5xx":     "count_5xx",
	"error":   "count_errors",
	"blocked": "count_blocked",
}

// ListPages returns the pages of a crawl matching f, ordered by id, along
// with the total number of matches (ignoring Limit/Offset).
func (s *Store) ListPages(crawlID int64, f PageFilter) ([]Page, int, error) {
	where := []string{"crawl_id = ?"}
	args := []any{crawlID}

	counterColumn, validStatus := pageFilterCounterColumn[f.Status]
	if !validStatus {
		return nil, 0, fmt.Errorf("status no válido")
	}
	switch f.Status {
	case "":
		// no filter
	case "2xx":
		where = append(where, "status BETWEEN 200 AND 299")
	case "3xx":
		where = append(where, "status BETWEEN 300 AND 399")
	case "4xx":
		where = append(where, "status BETWEEN 400 AND 499")
	case "5xx":
		where = append(where, "status BETWEEN 500 AND 599")
	case "error":
		where = append(where, "status = 0 AND blocked = 0")
	case "blocked":
		where = append(where, "blocked = 1")
	}

	if f.Query != "" {
		pattern := "%" + escapeLike(f.Query) + "%"
		where = append(where, "(url LIKE ? ESCAPE '\\' OR title LIKE ? ESCAPE '\\')")
		args = append(args, pattern, pattern)
	}

	whereClause := strings.Join(where, " AND ")

	var total int
	if f.Query == "" {
		// counterColumn is one of the fixed literals in
		// pageFilterCounterColumn, never caller input.
		err := s.r.QueryRow("SELECT "+counterColumn+" FROM crawls WHERE id = ?", crawlID).Scan(&total)
		if errors.Is(err, sql.ErrNoRows) {
			total = 0 // unknown crawl: no pages either, and callers check existence themselves
		} else if err != nil {
			return nil, 0, fmt.Errorf("store: count pages in crawl %d: %w", crawlID, err)
		}
	} else {
		countQuery := "SELECT COUNT(*) FROM pages WHERE " + whereClause
		if err := s.r.QueryRow(countQuery, args...).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("store: count pages in crawl %d: %w", crawlID, err)
		}
	}

	limit := clampLimit(f.Limit)

	selectQuery := `SELECT id, crawl_id, url, depth, status, content_type, size, duration_ms,
			title, description, canonical, meta_robots, noindex, nofollow,
			h1, redirect_to, error, blocked, fetched_at
		 FROM pages WHERE ` + whereClause + ` ORDER BY id LIMIT ? OFFSET ?`
	selectArgs := append(append([]any{}, args...), limit, f.Offset)

	rows, err := s.r.Query(selectQuery, selectArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list pages in crawl %d: %w", crawlID, err)
	}
	defer rows.Close()

	var out []Page
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("store: list pages in crawl %d: %w", crawlID, err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("store: list pages in crawl %d: %w", crawlID, err)
	}
	return out, total, nil
}

// escapeLike escapes the characters that are significant to SQL LIKE
// (\, % and _) so that a user-supplied substring is matched literally.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

func scanPage(row rowScanner) (*Page, error) {
	var (
		p          Page
		noindex    int
		nofollow   int
		blocked    int
		fetchedAtS string
	)
	if err := row.Scan(
		&p.ID, &p.CrawlID, &p.URL, &p.Depth, &p.Status, &p.ContentType, &p.Size, &p.DurationMs,
		&p.Title, &p.Description, &p.Canonical, &p.MetaRobots, &noindex, &nofollow,
		&p.H1, &p.RedirectTo, &p.Error, &blocked, &fetchedAtS,
	); err != nil {
		return nil, err
	}
	p.NoIndex = noindex != 0
	p.NoFollow = nofollow != 0
	p.Blocked = blocked != 0
	t, err := parseTime(fetchedAtS)
	if err != nil {
		return nil, err
	}
	p.FetchedAt = t
	return &p, nil
}
