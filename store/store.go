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
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned by getters when the requested row does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps a SQLite database handle.
type Store struct {
	db *sql.DB
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
		robots_txt  TEXT NOT NULL DEFAULT ''
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
func Open(path string) (*Store, error) {
	if err := checkDirWritable(path); err != nil {
		return nil, err
	}
	ensureSQLiteTempDir(filepath.Dir(path), sqliteTempCandidates())
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// modernc.org/sqlite applies _pragma DSN values per new connection, so a
	// pool of more than one connection could hand out a connection where
	// foreign_keys was not (yet) enabled. A single connection keeps behavior
	// predictable and is enough for eanbot's workload.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
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
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("store: init schema: %w", err)
		}
	}
	now := nowString()
	if _, err := s.db.Exec(
		`UPDATE crawls SET status = 'failed', error = 'interrumpido', finished_at = ? WHERE status = 'running'`,
		now,
	); err != nil {
		return fmt.Errorf("store: mark interrupted crawls: %w", err)
	}
	return nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
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
