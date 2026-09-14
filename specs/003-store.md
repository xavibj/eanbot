# 003 — Paquete `store` (SQLite)

Paquete `xavi.net/eanbot/store`. Único punto de acceso a la BBDD. Depende solo
de stdlib y `modernc.org/sqlite`. No importa `crawler` (tiene sus propios
tipos de registro; la conversión la hacen `server` y `cmd`).

## Apertura

```go
func Open(path string) (*Store, error)  // crea el fichero y el esquema si no existen
func (s *Store) Close() error
```
DSN `file:<path>?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`,
`SetMaxOpenConns(1)`. Esquema idempotente (`CREATE TABLE IF NOT EXISTS`).
Al abrir, cualquier crawl en estado `running` pasa a `failed` con
`error = "interrumpido"` (el proceso anterior murió).

Antes de abrir, `Open` comprueba que el directorio de `path` existe y es
escribible (crea y borra un fichero de sondeo); si no, devuelve un error en
español que nombra el directorio, el uid del proceso y la solución para
Docker (`chown 65532:65532 <dir>`), en vez del críptico
`attempt to write a readonly database (1544)` de SQLite en WAL.

`Open` también garantiza un directorio temporal para SQLite: las consultas
de resumen y enlaces rotos ordenan y agrupan tablas grandes y se vuelcan a
ficheros temporales; SQLite los busca en `SQLITE_TMPDIR`, `TMPDIR`,
`/var/tmp`, `/usr/tmp`, `/tmp` y el cwd, y si ninguno es escribible falla con
`disk I/O error (6410)` (`SQLITE_IOERR_GETTEMPPATH`), que es lo que ocurre en
una imagen `scratch`. Si `SQLITE_TMPDIR` no está definida y ninguno de esos
candidatos es un directorio escribible, `Open` fija `SQLITE_TMPDIR` al
directorio de la BBDD antes de abrir la conexión.

## Esquema

```sql
CREATE TABLE crawls (
  id          INTEGER PRIMARY KEY,
  seed        TEXT NOT NULL,
  status      TEXT NOT NULL,              -- running | done | failed | cancelled
  config      TEXT NOT NULL,              -- JSON (ver 004, objeto crawl.config)
  started_at  TEXT NOT NULL,              -- RFC3339 UTC
  finished_at TEXT,
  pages_count INTEGER NOT NULL DEFAULT 0, -- filas en pages (incluye bloqueadas y errores)
  error       TEXT NOT NULL DEFAULT '',
  robots_txt  TEXT NOT NULL DEFAULT ''
);
CREATE TABLE pages (
  id           INTEGER PRIMARY KEY,
  crawl_id     INTEGER NOT NULL REFERENCES crawls(id) ON DELETE CASCADE,
  url          TEXT NOT NULL,
  depth        INTEGER NOT NULL,
  status       INTEGER NOT NULL,          -- 0 si bloqueada/error
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
);
CREATE INDEX pages_crawl_status ON pages(crawl_id, status);
CREATE TABLE links (
  id           INTEGER PRIMARY KEY,
  crawl_id     INTEGER NOT NULL REFERENCES crawls(id) ON DELETE CASCADE,
  from_page_id INTEGER NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
  to_url       TEXT NOT NULL,
  text         TEXT NOT NULL DEFAULT '',
  nofollow     INTEGER NOT NULL DEFAULT 0,
  in_scope     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX links_crawl_to ON links(crawl_id, to_url);
CREATE INDEX links_from ON links(from_page_id);
```

## Tipos y API

```go
var ErrNotFound = errors.New("not found")

type Crawl struct {
    ID         int64     `json:"id"`
    Seed       string    `json:"seed"`
    Status     string    `json:"status"`
    Config     json.RawMessage `json:"config"`
    StartedAt  time.Time `json:"started_at"`
    FinishedAt *time.Time `json:"finished_at"`   // null si no ha acabado
    PagesCount int       `json:"pages_count"`
    Error      string    `json:"error"`
    RobotsTxt  string    `json:"robots_txt"`
}

type Page struct {
    ID          int64  `json:"id"`
    CrawlID     int64  `json:"crawl_id"`
    URL         string `json:"url"`
    Depth       int    `json:"depth"`
    Status      int    `json:"status"`
    ContentType string `json:"content_type"`
    Size        int64  `json:"size"`
    DurationMs  int64  `json:"duration_ms"`
    Title       string `json:"title"`
    Description string `json:"description"`
    Canonical   string `json:"canonical"`
    MetaRobots  string `json:"meta_robots"`
    NoIndex     bool   `json:"noindex"`
    NoFollow    bool   `json:"nofollow"`
    H1          string `json:"h1"`
    RedirectTo  string `json:"redirect_to"`
    Error       string `json:"error"`
    Blocked     bool   `json:"blocked"`
    FetchedAt   time.Time `json:"fetched_at"`
}

type Link struct {
    FromPageID int64  `json:"from_page_id"`
    FromURL    string `json:"from_url"`   // rellenado en consultas de inlinks
    ToURL      string `json:"to_url"`
    Text       string `json:"text"`
    NoFollow   bool   `json:"nofollow"`
    InScope    bool   `json:"in_scope"`
}

type PageFilter struct {
    Status string // "", "2xx", "3xx", "4xx", "5xx", "error" (status 0 && !blocked), "blocked"
    Query  string // LIKE %q% sobre url o title
    Limit  int    // por defecto 100, máx. 1000
    Offset int
}

type Summary struct {
    Total    int            `json:"total"`
    Status2xx int           `json:"status_2xx"`
    Status3xx int           `json:"status_3xx"`
    Status4xx int           `json:"status_4xx"`
    Status5xx int           `json:"status_5xx"`
    Errors   int            `json:"errors"`
    Blocked  int            `json:"blocked"`
    NoIndex  int            `json:"noindex"`
    ContentTypes map[string]int `json:"content_types"`
    MaxDepth int            `json:"max_depth"`
    AvgDurationMs int64     `json:"avg_duration_ms"` // solo páginas con status > 0
}

type BrokenLink struct {
    Page      Page   `json:"page"`        // la página rota (status>=400 o error)
    Referrers []Link `json:"referrers"`   // enlaces cuyo to_url == page.url (con FromURL)
}

func (s *Store) CreateCrawl(seed string, config json.RawMessage) (*Crawl, error) // status running, started_at now UTC
func (s *Store) SetRobots(crawlID int64, robots string) error
func (s *Store) FinishCrawl(crawlID int64, status, errMsg string) error // status ∈ done|failed|cancelled; finished_at now; ErrNotFound
func (s *Store) GetCrawl(id int64) (*Crawl, error)                     // ErrNotFound
func (s *Store) ListCrawls() ([]Crawl, error)                          // más recientes primero
func (s *Store) DeleteCrawl(id int64) error                            // cascade; ErrNotFound
func (s *Store) AddPage(p Page, links []Link) (int64, error)           // tx: insert page + links, pages_count++; URL duplicada en el mismo crawl → error
func (s *Store) GetPage(crawlID, pageID int64) (*Page, []Link /*out*/, []Link /*in*/, error) // ErrNotFound
func (s *Store) FindPageByURL(crawlID int64, url string) (*Page, error)
func (s *Store) ListPages(crawlID int64, f PageFilter) ([]Page, int /*total con filtro*/, error)
func (s *Store) Summarize(crawlID int64) (*Summary, error)             // ErrNotFound si no existe el crawl
func (s *Store) BrokenLinks(crawlID int64) ([]BrokenLink, error)       // ordenado por url; referrers ordenados por from_url
func (s *Store) Outlinks(crawlID int64) (map[string]int, error)        // opcional: to_url → nº de referrers (no requerido en v1)
```

`FinishCrawl` sobre un crawl que ya no está `running` no cambia nada y no
devuelve error (idempotente), salvo que no exista (ErrNotFound).

## Tests exigidos

Tabla, BBDD real en `t.TempDir()`: esquema idempotente (Open dos veces);
CreateCrawl/GetCrawl/ListCrawls orden; FinishCrawl estados e idempotencia;
running→failed al reabrir; AddPage incrementa `pages_count` y rechaza URL
duplicada; GetPage devuelve out/in links con FromURL; ListPages filtros
(cada valor de Status, Query, Limit/Offset, total); Summarize contadores y
content_types; BrokenLinks con referrers; DeleteCrawl cascade; ErrNotFound en
todos los getters.
