package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AddPage inserts a page and its outgoing links in a single transaction and
// increments the owning crawl's pages_count. It returns the new page id.
// Inserting a page whose URL already exists in the same crawl fails (the
// UNIQUE(crawl_id, url) constraint).
func (s *Store) AddPage(p Page, links []Link) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: add page: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	res, err := tx.Exec(
		`INSERT INTO pages (
			crawl_id, url, depth, status, content_type, size, duration_ms,
			title, description, canonical, meta_robots, noindex, nofollow,
			h1, redirect_to, error, blocked, fetched_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.CrawlID, p.URL, p.Depth, p.Status, p.ContentType, p.Size, p.DurationMs,
		p.Title, p.Description, p.Canonical, p.MetaRobots, boolToInt(p.NoIndex), boolToInt(p.NoFollow),
		p.H1, p.RedirectTo, p.Error, boolToInt(p.Blocked), p.FetchedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("store: add page %q to crawl %d: %w", p.URL, p.CrawlID, err)
	}
	pageID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: add page %q to crawl %d: %w", p.URL, p.CrawlID, err)
	}

	for _, l := range links {
		if _, err := tx.Exec(
			`INSERT INTO links (crawl_id, from_page_id, to_url, text, nofollow, in_scope) VALUES (?, ?, ?, ?, ?, ?)`,
			p.CrawlID, pageID, l.ToURL, l.Text, boolToInt(l.NoFollow), boolToInt(l.InScope),
		); err != nil {
			return 0, fmt.Errorf("store: add link %q from page %q: %w", l.ToURL, p.URL, err)
		}
	}

	if _, err := tx.Exec(`UPDATE crawls SET pages_count = pages_count + 1 WHERE id = ?`, p.CrawlID); err != nil {
		return 0, fmt.Errorf("store: increment pages_count for crawl %d: %w", p.CrawlID, err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: add page %q to crawl %d: %w", p.URL, p.CrawlID, err)
	}
	return pageID, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// GetPage returns a page plus its outgoing links (out) and the links from
// other pages in the same crawl that point to it (in, with FromURL filled).
func (s *Store) GetPage(crawlID, pageID int64) (*Page, []Link, []Link, error) {
	row := s.db.QueryRow(
		`SELECT id, crawl_id, url, depth, status, content_type, size, duration_ms,
			title, description, canonical, meta_robots, noindex, nofollow,
			h1, redirect_to, error, blocked, fetched_at
		 FROM pages WHERE crawl_id = ? AND id = ?`,
		crawlID, pageID,
	)
	p, err := scanPage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("store: get page %d in crawl %d: %w", pageID, crawlID, err)
	}

	out, err := s.linksFromPage(p.ID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("store: get page %d in crawl %d: %w", pageID, crawlID, err)
	}
	in, err := s.linksToURL(crawlID, p.URL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("store: get page %d in crawl %d: %w", pageID, crawlID, err)
	}
	return p, out, in, nil
}

func (s *Store) linksFromPage(pageID int64) ([]Link, error) {
	rows, err := s.db.Query(
		`SELECT from_page_id, to_url, text, nofollow, in_scope FROM links WHERE from_page_id = ? ORDER BY id`,
		pageID,
	)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		l.NoFollow = nofollow != 0
		l.InScope = inScope != 0
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) linksToURL(crawlID int64, url string) ([]Link, error) {
	rows, err := s.db.Query(
		`SELECT l.from_page_id, p.url, l.to_url, l.text, l.nofollow, l.in_scope
		 FROM links l JOIN pages p ON p.id = l.from_page_id
		 WHERE l.crawl_id = ? AND l.to_url = ?
		 ORDER BY p.url, l.id`,
		crawlID, url,
	)
	if err != nil {
		return nil, err
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
			return nil, err
		}
		l.NoFollow = nofollow != 0
		l.InScope = inScope != 0
		out = append(out, l)
	}
	return out, rows.Err()
}

// FindPageByURL returns the page with the given URL in the given crawl.
func (s *Store) FindPageByURL(crawlID int64, url string) (*Page, error) {
	row := s.db.QueryRow(
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

// ListPages returns the pages of a crawl matching f, ordered by id, along
// with the total number of matches (ignoring Limit/Offset).
func (s *Store) ListPages(crawlID int64, f PageFilter) ([]Page, int, error) {
	where := []string{"crawl_id = ?"}
	args := []any{crawlID}

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
	default:
		return nil, 0, fmt.Errorf("status no válido")
	}

	if f.Query != "" {
		pattern := "%" + escapeLike(f.Query) + "%"
		where = append(where, "(url LIKE ? ESCAPE '\\' OR title LIKE ? ESCAPE '\\')")
		args = append(args, pattern, pattern)
	}

	whereClause := strings.Join(where, " AND ")

	var total int
	countQuery := "SELECT COUNT(*) FROM pages WHERE " + whereClause
	if err := s.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: count pages in crawl %d: %w", crawlID, err)
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	selectQuery := `SELECT id, crawl_id, url, depth, status, content_type, size, duration_ms,
			title, description, canonical, meta_robots, noindex, nofollow,
			h1, redirect_to, error, blocked, fetched_at
		 FROM pages WHERE ` + whereClause + ` ORDER BY id LIMIT ? OFFSET ?`
	selectArgs := append(append([]any{}, args...), limit, f.Offset)

	rows, err := s.db.Query(selectQuery, selectArgs...)
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
