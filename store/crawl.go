package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// CreateCrawl inserts a new crawl in "running" state and returns it. It
// starts with counters_ok = 1: a freshly created crawl has no pages yet, so
// its (zero-valued) counters already reflect its pages, unlike a crawl
// migrated from before specs/003-store.md's "Contadores" (see
// backfillCounters in store.go).
func (s *Store) CreateCrawl(seed string, config json.RawMessage) (*Crawl, error) {
	startedAt := nowString()
	res, err := s.w.Exec(
		`INSERT INTO crawls (seed, status, config, started_at, counters_ok) VALUES (?, 'running', ?, ?, 1)`,
		seed, string(config), startedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store: create crawl: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("store: create crawl: %w", err)
	}
	return s.GetCrawl(id)
}

// SetRobots stores the robots.txt content fetched for a crawl.
func (s *Store) SetRobots(crawlID int64, robots string) error {
	res, err := s.w.Exec(`UPDATE crawls SET robots_txt = ? WHERE id = ?`, robots, crawlID)
	if err != nil {
		return fmt.Errorf("store: set robots for crawl %d: %w", crawlID, err)
	}
	return checkRowsAffected(res, crawlID)
}

// FinishCrawl marks a running crawl as done, failed or cancelled. It is a
// no-op (no error) if the crawl exists but is no longer running.
func (s *Store) FinishCrawl(crawlID int64, status, errMsg string) error {
	finishedAt := nowString()
	res, err := s.w.Exec(
		`UPDATE crawls SET status = ?, error = ?, finished_at = ? WHERE id = ? AND status = 'running'`,
		status, errMsg, finishedAt, crawlID,
	)
	if err != nil {
		return fmt.Errorf("store: finish crawl %d: %w", crawlID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: finish crawl %d: %w", crawlID, err)
	}
	if n > 0 {
		// Best effort: a long crawl can grow the WAL substantially between
		// checkpoints (see "Checkpoints" in specs/003-store.md); truncate it
		// now that the crawl is done. store has no logger, and a failed
		// checkpoint does not affect the data already committed.
		_ = s.Checkpoint("TRUNCATE")
		return nil
	}
	// No row updated: either the crawl does not exist, or it exists but is
	// already finished (idempotent no-op). Tell those apart.
	_, err = s.GetCrawl(crawlID)
	return err
}

// GetCrawl returns a crawl by id, or ErrNotFound.
func (s *Store) GetCrawl(id int64) (*Crawl, error) {
	row := s.r.QueryRow(
		`SELECT id, seed, status, config, started_at, finished_at, pages_count, error, robots_txt
		 FROM crawls WHERE id = ?`, id,
	)
	c, err := scanCrawl(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get crawl %d: %w", id, err)
	}
	return c, nil
}

// ListCrawls returns all crawls, most recently started first.
func (s *Store) ListCrawls() ([]Crawl, error) {
	rows, err := s.r.Query(
		`SELECT id, seed, status, config, started_at, finished_at, pages_count, error, robots_txt
		 FROM crawls ORDER BY started_at DESC, id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list crawls: %w", err)
	}
	defer rows.Close()

	var out []Crawl
	for rows.Next() {
		c, err := scanCrawl(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list crawls: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list crawls: %w", err)
	}
	return out, nil
}

// DeleteCrawl removes a crawl and, via ON DELETE CASCADE, its pages and
// links.
func (s *Store) DeleteCrawl(id int64) error {
	res, err := s.w.Exec(`DELETE FROM crawls WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete crawl %d: %w", id, err)
	}
	if err := checkRowsAffected(res, id); err != nil {
		return err
	}
	// Best effort, same reasoning as FinishCrawl: a deleted crawl can free a
	// lot of rows at once, worth reclaiming from the WAL right away.
	_ = s.Checkpoint("TRUNCATE")
	return nil
}

// crawlExists reports whether a crawl with the given id exists.
func (s *Store) crawlExists(id int64) (bool, error) {
	var n int
	err := s.r.QueryRow(`SELECT 1 FROM crawls WHERE id = ?`, id).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: check crawl %d exists: %w", id, err)
	}
	return true, nil
}

func checkRowsAffected(res sql.Result, id int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: id %d: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanCrawl(row rowScanner) (*Crawl, error) {
	var (
		c          Crawl
		config     string
		startedAt  string
		finishedAt sql.NullString
	)
	if err := row.Scan(&c.ID, &c.Seed, &c.Status, &config, &startedAt, &finishedAt, &c.PagesCount, &c.Error, &c.RobotsTxt); err != nil {
		return nil, err
	}
	c.Config = json.RawMessage(config)
	t, err := parseTime(startedAt)
	if err != nil {
		return nil, err
	}
	c.StartedAt = t
	if finishedAt.Valid {
		ft, err := parseTime(finishedAt.String)
		if err != nil {
			return nil, err
		}
		c.FinishedAt = &ft
	}
	return &c, nil
}
