// Package store is the single point of access to eanbot's SQLite database.
// It depends only on the standard library and modernc.org/sqlite (a pure Go
// driver, no cgo). It knows nothing about the crawler package; server and
// cmd convert between crawler types and store types.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned by getters when the requested row does not exist.
var ErrNotFound = errors.New("not found")

// checkpointInterval controls how often Open's background goroutine issues
// PRAGMA wal_checkpoint(RESTART) (see "Checkpoints" in specs/003-store.md).
// It is a package variable rather than a constant purely so store's own
// tests can shrink it (e.g. to 20ms) to observe a periodic checkpoint
// without waiting 60 real seconds.
var checkpointInterval = 60 * time.Second

// Store wraps two SQLite database handles over the same file: one for
// writes (and schema init), one for reads. See Open for why.
type Store struct {
	w *sql.DB // writes: CreateCrawl, SetRobots, FinishCrawl, AddPage(s), DeleteCrawl, init, Checkpoint. SetMaxOpenConns(1).
	r *sql.DB // reads: everything else. SetMaxOpenConns(4).

	stopCheckpoint chan struct{}
	checkpointWG   sync.WaitGroup
}

// schemaStatements creates the schema if it does not exist yet. Each
// statement is executed separately since the driver does not support
// multiple statements in a single Exec call.
var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS crawls (
		id          INTEGER PRIMARY KEY,
		seed        TEXT NOT NULL,
		status      TEXT NOT NULL,
		config      TEXT NOT NULL,
		started_at  TEXT NOT NULL,
		finished_at TEXT,
		pages_count INTEGER NOT NULL DEFAULT 0,
		error       TEXT NOT NULL DEFAULT '',
		robots_txt  TEXT NOT NULL DEFAULT '',
		count_2xx     INTEGER NOT NULL DEFAULT 0,
		count_3xx     INTEGER NOT NULL DEFAULT 0,
		count_4xx     INTEGER NOT NULL DEFAULT 0,
		count_5xx     INTEGER NOT NULL DEFAULT 0,
		count_errors  INTEGER NOT NULL DEFAULT 0,
		count_blocked INTEGER NOT NULL DEFAULT 0,
		count_noindex INTEGER NOT NULL DEFAULT 0,
		count_via_nofollow INTEGER NOT NULL DEFAULT 0,
		max_depth     INTEGER NOT NULL DEFAULT 0,
		duration_sum  INTEGER NOT NULL DEFAULT 0,
		duration_n    INTEGER NOT NULL DEFAULT 0,
		counters_ok   INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS pages (
		id           INTEGER PRIMARY KEY,
		crawl_id     INTEGER NOT NULL REFERENCES crawls(id) ON DELETE CASCADE,
		url          TEXT NOT NULL,
		depth        INTEGER NOT NULL,
		status       INTEGER NOT NULL,
		content_type TEXT NOT NULL DEFAULT '',
		size         INTEGER NOT NULL DEFAULT 0,
		duration_ms  INTEGER NOT NULL DEFAULT 0,
		title        TEXT NOT NULL DEFAULT '',
		description  TEXT NOT NULL DEFAULT '',
		canonical    TEXT NOT NULL DEFAULT '',
		meta_robots  TEXT NOT NULL DEFAULT '',
		x_robots_tag TEXT NOT NULL DEFAULT '',
		via_nofollow INTEGER NOT NULL DEFAULT 0,
		noindex      INTEGER NOT NULL DEFAULT 0,
		nofollow     INTEGER NOT NULL DEFAULT 0,
		h1           TEXT NOT NULL DEFAULT '',
		redirect_to  TEXT NOT NULL DEFAULT '',
		error        TEXT NOT NULL DEFAULT '',
		blocked      INTEGER NOT NULL DEFAULT 0,
		fetched_at   TEXT NOT NULL,
		UNIQUE (crawl_id, url)
	)`,
	`CREATE INDEX IF NOT EXISTS pages_crawl_status ON pages(crawl_id, status)`,
	`CREATE TABLE IF NOT EXISTS crawl_content_types (
		crawl_id     INTEGER NOT NULL REFERENCES crawls(id) ON DELETE CASCADE,
		content_type TEXT NOT NULL,
		n            INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (crawl_id, content_type)
	)`,
	`CREATE TABLE IF NOT EXISTS links (
		id           INTEGER PRIMARY KEY,
		crawl_id     INTEGER NOT NULL REFERENCES crawls(id) ON DELETE CASCADE,
		from_page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
		to_url       TEXT NOT NULL,
		text         TEXT NOT NULL DEFAULT '',
		nofollow     INTEGER NOT NULL DEFAULT 0,
		in_scope     INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS links_crawl_to ON links(crawl_id, to_url)`,
	`CREATE INDEX IF NOT EXISTS links_from ON links(from_page_id)`,
}

// Open opens (creating if needed) the SQLite database at path, applies the
// schema and marks any crawl left in "running" state as failed (the process
// that owned it died before finishing).
// Open uses two separate connection pools over the same DSN: a write pool
// limited to a single connection (SQLite allows only one writer at a time
// anyway, and this serializes CreateCrawl/SetRobots/FinishCrawl/AddPage/
// DeleteCrawl/init so they never race each other) and a read pool of up to
// four connections. WAL mode lets readers proceed against the last
// committed snapshot while a write transaction is open, so a long crawl
// insert never makes the UI wait, and a long report query never makes the
// crawl wait. modernc.org/sqlite applies _pragma DSN values per new
// connection, so both pools see the same pragmas.
func Open(path string) (*Store, error) {
	if err := checkDirWritable(path); err != nil {
		return nil, err
	}
	ensureSQLiteTempDir(filepath.Dir(path), sqliteTempCandidates())
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=journal_size_limit(67108864)", path)

	w, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	w.SetMaxOpenConns(1)

	r, err := sql.Open("sqlite", dsn)
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	r.SetMaxOpenConns(4)

	s := &Store{w: w, r: r, stopCheckpoint: make(chan struct{})}
	if err := s.init(); err != nil {
		w.Close()
		r.Close()
		return nil, err
	}

	s.checkpointWG.Add(1)
	go s.runCheckpointLoop()

	return s, nil
}

// runCheckpointLoop periodically truncates the WAL back to
// journal_size_limit by restarting it, so that many hours of continuous
// reads never let the WAL grow unbounded (see "Checkpoints" in
// specs/003-store.md: a real crawl once grew the WAL to 45 GB from
// checkpoint starvation). It stops when Close closes stopCheckpoint.
func (s *Store) runCheckpointLoop() {
	defer s.checkpointWG.Done()
	ticker := time.NewTicker(checkpointInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// A failed checkpoint (e.g. a long reader still holding the WAL
			// open) does not affect correctness, only WAL growth, and store
			// has no logger to report it to; the next tick tries again.
			_ = s.Checkpoint("RESTART")
		case <-s.stopCheckpoint:
			return
		}
	}
}

// checkDirWritable verifies that the directory holding the database file is
// writable by the current process. SQLite in WAL mode needs to create the
// -wal and -shm files next to the database, and an unwritable directory
// surfaces as the cryptic "attempt to write a readonly database (1544)"
// (SQLITE_READONLY_DIRECTORY). This is the classic Docker bind-mount problem:
// the container runs as uid 65532 and the host directory is owned by someone
// else. Say so explicitly.
func checkDirWritable(path string) error {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("no es un directorio")
		}
		return fmt.Errorf("store: el directorio de la base de datos %q no existe: %w", dir, err)
	}
	probe, err := os.CreateTemp(dir, ".eanbot-write-probe-*")
	if err != nil {
		return fmt.Errorf("store: el directorio de la base de datos %q no es escribible por el uid %d (SQLite en modo WAL necesita crear ficheros junto a la BBDD; en Docker: chown 65532:65532 %s): %w",
			dir, os.Getuid(), dir, err)
	}
	name := probe.Name()
	probe.Close()
	os.Remove(name)
	return nil
}

// sqliteTempCandidates lists the directories SQLite tries for temporary
// files when SQLITE_TMPDIR is unset, in SQLite's own order.
func sqliteTempCandidates() []string {
	return []string{os.Getenv("TMPDIR"), "/var/tmp", "/usr/tmp", "/tmp", "."}
}

// ensureSQLiteTempDir makes sure SQLite will find a writable temporary
// directory. Large sorts and group-bys (summaries, broken links) spill to temp
// files; when none of SQLite's candidate directories is writable, as in a
// scratch container image, every such query fails with "disk I/O error
// (6410)" (SQLITE_IOERR_GETTEMPPATH). If SQLITE_TMPDIR is unset and no
// candidate is a writable directory, point SQLITE_TMPDIR at the database
// directory, which is known to be writable.
func ensureSQLiteTempDir(dbDir string, candidates []string) {
	if os.Getenv("SQLITE_TMPDIR") != "" {
		return
	}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if dirWritable(dir) {
			return
		}
	}
	os.Setenv("SQLITE_TMPDIR", dbDir)
}

func dirWritable(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	probe, err := os.CreateTemp(dir, ".eanbot-write-probe-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	probe.Close()
	os.Remove(name)
	return true
}

func (s *Store) init() error {
	for _, stmt := range schemaStatements {
		if _, err := s.w.Exec(stmt); err != nil {
			return fmt.Errorf("store: init schema: %w", err)
		}
	}
	if err := s.migrateCrawlsColumns(); err != nil {
		return err
	}
	if err := s.migratePagesColumns(); err != nil {
		return err
	}
	if err := s.backfillCounters(); err != nil {
		return err
	}
	now := nowString()
	if _, err := s.w.Exec(
		`UPDATE crawls SET status = 'failed', error = 'interrumpido', finished_at = ? WHERE status = 'running'`,
		now,
	); err != nil {
		return fmt.Errorf("store: mark interrupted crawls: %w", err)
	}
	return nil
}

// crawlCounterColumns are the columns this version of eanbot adds to crawls
// for O(1) summaries (see "Contadores" in specs/003-store.md). A database
// created before they existed needs them added via ALTER TABLE; new
// databases get them straight from schemaStatements, so adding a column
// that is already there (name already in PRAGMA table_info) is skipped.
var crawlCounterColumns = []struct {
	name string
	ddl  string
}{
	{"count_2xx", "ALTER TABLE crawls ADD COLUMN count_2xx INTEGER NOT NULL DEFAULT 0"},
	{"count_3xx", "ALTER TABLE crawls ADD COLUMN count_3xx INTEGER NOT NULL DEFAULT 0"},
	{"count_4xx", "ALTER TABLE crawls ADD COLUMN count_4xx INTEGER NOT NULL DEFAULT 0"},
	{"count_5xx", "ALTER TABLE crawls ADD COLUMN count_5xx INTEGER NOT NULL DEFAULT 0"},
	{"count_errors", "ALTER TABLE crawls ADD COLUMN count_errors INTEGER NOT NULL DEFAULT 0"},
	{"count_blocked", "ALTER TABLE crawls ADD COLUMN count_blocked INTEGER NOT NULL DEFAULT 0"},
	{"count_noindex", "ALTER TABLE crawls ADD COLUMN count_noindex INTEGER NOT NULL DEFAULT 0"},
	{"count_via_nofollow", "ALTER TABLE crawls ADD COLUMN count_via_nofollow INTEGER NOT NULL DEFAULT 0"},
	{"max_depth", "ALTER TABLE crawls ADD COLUMN max_depth INTEGER NOT NULL DEFAULT 0"},
	{"duration_sum", "ALTER TABLE crawls ADD COLUMN duration_sum INTEGER NOT NULL DEFAULT 0"},
	{"duration_n", "ALTER TABLE crawls ADD COLUMN duration_n INTEGER NOT NULL DEFAULT 0"},
	{"counters_ok", "ALTER TABLE crawls ADD COLUMN counters_ok INTEGER NOT NULL DEFAULT 0"},
}

// migrateCrawlsColumns adds any of crawlCounterColumns missing from an
// existing crawls table.
func (s *Store) migrateCrawlsColumns() error {
	existing, err := s.tableColumnNames("crawls")
	if err != nil {
		return err
	}
	for _, col := range crawlCounterColumns {
		if existing[col.name] {
			continue
		}
		if _, err := s.w.Exec(col.ddl); err != nil {
			return fmt.Errorf("store: add column %s to crawls: %w", col.name, err)
		}
	}
	return nil
}

// tableColumnNames returns the set of column names table currently has, via
// PRAGMA table_info. table is always one of the fixed literals "crawls" or
// "pages", never caller input.
func (s *Store) tableColumnNames(table string) (map[string]bool, error) {
	rows, err := s.w.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, fmt.Errorf("store: inspect %s schema: %w", table, err)
	}
	defer rows.Close()

	existing := map[string]bool{}
	for rows.Next() {
		var (
			cid, notNull, pk int
			name, ctype      string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("store: inspect %s schema: %w", table, err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: inspect %s schema: %w", table, err)
	}
	return existing, nil
}

// pagesCounterColumns are the columns this version of eanbot adds to pages
// (x_robots_tag, via_nofollow), added the same way as crawlCounterColumns:
// via ALTER TABLE on a pre-existing table, skipped when already present.
var pagesCounterColumns = []struct {
	name string
	ddl  string
}{
	{"x_robots_tag", "ALTER TABLE pages ADD COLUMN x_robots_tag TEXT NOT NULL DEFAULT ''"},
	{"via_nofollow", "ALTER TABLE pages ADD COLUMN via_nofollow INTEGER NOT NULL DEFAULT 0"},
}

// migratePagesColumns adds any of pagesCounterColumns missing from an
// existing pages table.
func (s *Store) migratePagesColumns() error {
	existing, err := s.tableColumnNames("pages")
	if err != nil {
		return err
	}
	for _, col := range pagesCounterColumns {
		if existing[col.name] {
			continue
		}
		if _, err := s.w.Exec(col.ddl); err != nil {
			return fmt.Errorf("store: add column %s to pages: %w", col.name, err)
		}
	}
	return nil
}

// backfillCounters recomputes the summary counters (and crawl_content_types)
// from pages for every crawl with counters_ok = 0 -- i.e. every crawl that
// existed before this version, whose counters were never maintained
// incrementally. Each crawl is backfilled in its own transaction, per
// specs/003-store.md.
func (s *Store) backfillCounters() error {
	rows, err := s.w.Query(`SELECT id FROM crawls WHERE counters_ok = 0`)
	if err != nil {
		return fmt.Errorf("store: find crawls needing counter backfill: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("store: find crawls needing counter backfill: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("store: find crawls needing counter backfill: %w", err)
	}
	rows.Close()

	for _, id := range ids {
		if err := s.backfillCrawlCounters(id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) backfillCrawlCounters(crawlID int64) error {
	tx, err := s.w.Begin()
	if err != nil {
		return fmt.Errorf("store: backfill counters for crawl %d: %w", crawlID, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after a successful Commit

	var (
		pagesCount, c2xx, c3xx, c4xx, c5xx, cErr, cBlocked, cNoIndex, cViaNoFollow, maxDepth int
		durSum, durN                                                                         int64
	)
	row := tx.QueryRow(
		`SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status BETWEEN 200 AND 299),
			COUNT(*) FILTER (WHERE status BETWEEN 300 AND 399),
			COUNT(*) FILTER (WHERE status BETWEEN 400 AND 499),
			COUNT(*) FILTER (WHERE status BETWEEN 500 AND 599),
			COUNT(*) FILTER (WHERE status = 0 AND blocked = 0),
			COUNT(*) FILTER (WHERE blocked = 1),
			COUNT(*) FILTER (WHERE noindex = 1),
			COUNT(*) FILTER (WHERE via_nofollow = 1),
			COALESCE(MAX(depth), 0),
			COALESCE(SUM(CASE WHEN status > 0 THEN duration_ms ELSE 0 END), 0),
			COUNT(*) FILTER (WHERE status > 0)
		 FROM pages WHERE crawl_id = ?`,
		crawlID,
	)
	if err := row.Scan(
		&pagesCount, &c2xx, &c3xx, &c4xx, &c5xx, &cErr, &cBlocked, &cNoIndex, &cViaNoFollow, &maxDepth, &durSum, &durN,
	); err != nil {
		return fmt.Errorf("store: backfill counters for crawl %d: %w", crawlID, err)
	}

	if _, err := tx.Exec(
		`UPDATE crawls SET
			pages_count = ?, count_2xx = ?, count_3xx = ?, count_4xx = ?, count_5xx = ?,
			count_errors = ?, count_blocked = ?, count_noindex = ?, count_via_nofollow = ?, max_depth = ?,
			duration_sum = ?, duration_n = ?, counters_ok = 1
		 WHERE id = ?`,
		pagesCount, c2xx, c3xx, c4xx, c5xx, cErr, cBlocked, cNoIndex, cViaNoFollow, maxDepth, durSum, durN, crawlID,
	); err != nil {
		return fmt.Errorf("store: backfill counters for crawl %d: %w", crawlID, err)
	}

	ctRows, err := tx.Query(
		`SELECT content_type, COUNT(*) FROM pages WHERE crawl_id = ? AND content_type != '' GROUP BY content_type`,
		crawlID,
	)
	if err != nil {
		return fmt.Errorf("store: backfill content types for crawl %d: %w", crawlID, err)
	}
	type ctCount struct {
		ct string
		n  int
	}
	var cts []ctCount
	for ctRows.Next() {
		var c ctCount
		if err := ctRows.Scan(&c.ct, &c.n); err != nil {
			ctRows.Close()
			return fmt.Errorf("store: backfill content types for crawl %d: %w", crawlID, err)
		}
		cts = append(cts, c)
	}
	if err := ctRows.Err(); err != nil {
		ctRows.Close()
		return fmt.Errorf("store: backfill content types for crawl %d: %w", crawlID, err)
	}
	ctRows.Close()

	for _, c := range cts {
		if _, err := tx.Exec(
			`INSERT INTO crawl_content_types (crawl_id, content_type, n) VALUES (?, ?, ?)
			 ON CONFLICT(crawl_id, content_type) DO UPDATE SET n = n + excluded.n`,
			crawlID, c.ct, c.n,
		); err != nil {
			return fmt.Errorf("store: backfill content types for crawl %d: %w", crawlID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: backfill counters for crawl %d: %w", crawlID, err)
	}
	return nil
}

// validCheckpointModes are the modes SQLite's wal_checkpoint pragma accepts.
var validCheckpointModes = map[string]bool{
	"PASSIVE": true, "FULL": true, "RESTART": true, "TRUNCATE": true,
}

// Checkpoint runs PRAGMA wal_checkpoint(mode) on the write pool. mode must
// be one of PASSIVE, FULL, RESTART or TRUNCATE.
func (s *Store) Checkpoint(mode string) error {
	if !validCheckpointModes[mode] {
		return fmt.Errorf("store: invalid checkpoint mode %q", mode)
	}
	if _, err := s.w.Exec("PRAGMA wal_checkpoint(" + mode + ")"); err != nil {
		return fmt.Errorf("store: checkpoint(%s): %w", mode, err)
	}
	return nil
}

// Close stops the background checkpoint goroutine, runs one last TRUNCATE
// checkpoint and closes both underlying database handles.
func (s *Store) Close() error {
	close(s.stopCheckpoint)
	s.checkpointWG.Wait()

	// Best effort: store has no logger, and a failed final checkpoint does
	// not affect the data already committed.
	_ = s.Checkpoint("TRUNCATE")

	errW := s.w.Close()
	errR := s.r.Close()
	if errW != nil {
		return errW
	}
	return errR
}

// clampLimit normalizes a caller-supplied limit for paginated queries: 0 or
// negative means the default of 100, and anything above 1000 is capped, per
// specs/003-store.md.
func clampLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

// nowString formats the current UTC time as used for all timestamp columns.
func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// parseTime parses a timestamp column value produced by nowString.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: parse time %q: %w", s, err)
	}
	return t, nil
}
