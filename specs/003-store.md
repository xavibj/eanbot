# 003 — Paquete `store` (SQLite)

Paquete `xavi.net/eanbot/store`. Único punto de acceso a la BBDD. Depende solo
de stdlib y `modernc.org/sqlite`. No importa `crawler` (tiene sus propios
tipos de registro; la conversión la hacen `server` y `cmd`).

## Apertura

```go
func Open(path string) (*Store, error)  // crea el fichero y el esquema si no existen
func (s *Store) Close() error
```
DSN `file:<path>?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`.

**Dos pools sobre el mismo fichero**: un pool de **escritura** con
`SetMaxOpenConns(1)` (todas las escrituras serializadas: `CreateCrawl`,
`SetRobots`, `FinishCrawl`, `AddPage`, `DeleteCrawl`, y el esquema/`init`) y un
pool de **lectura** con `SetMaxOpenConns(4)` para todo lo demás. WAL permite
lectores concurrentes con un escritor, así que la interfaz no espera a las
inserciones del rastreo ni una consulta larga bloquea al rastreo. Las pragmas
del DSN se aplican por conexión, así que valen para ambos pools. `Close`
cierra los dos. Esquema idempotente (`CREATE TABLE IF NOT EXISTS`).
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
  robots_txt  TEXT NOT NULL DEFAULT '',
  -- contadores mantenidos por AddPages (resumen O(1)); ver "Contadores"
  count_2xx     INTEGER NOT NULL DEFAULT 0,
  count_3xx     INTEGER NOT NULL DEFAULT 0,
  count_4xx     INTEGER NOT NULL DEFAULT 0,
  count_5xx     INTEGER NOT NULL DEFAULT 0,
  count_errors  INTEGER NOT NULL DEFAULT 0,   -- status 0 y no bloqueada
  count_blocked INTEGER NOT NULL DEFAULT 0,
  count_noindex INTEGER NOT NULL DEFAULT 0,
  count_via_nofollow INTEGER NOT NULL DEFAULT 0,
  max_depth     INTEGER NOT NULL DEFAULT 0,
  duration_sum  INTEGER NOT NULL DEFAULT 0,   -- suma de duration_ms de páginas con status > 0
  duration_n    INTEGER NOT NULL DEFAULT 0,   -- nº de páginas con status > 0
  counters_ok   INTEGER NOT NULL DEFAULT 0    -- 1 cuando los contadores reflejan pages (migración)
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
);
CREATE INDEX pages_crawl_status ON pages(crawl_id, status);
CREATE TABLE crawl_content_types (
  crawl_id     INTEGER NOT NULL REFERENCES crawls(id) ON DELETE CASCADE,
  content_type TEXT NOT NULL,
  n            INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (crawl_id, content_type)
);
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
    XRobotsTag  string `json:"x_robots_tag"`
    ViaNoFollow bool   `json:"via_nofollow"`
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

type PageDetail struct {
    Page          Page   `json:"page"`
    Outlinks      []Link `json:"outlinks"`       // como máximo LinkLimit (500), orden por id
    Inlinks       []Link `json:"inlinks"`        // como máximo LinkLimit (500), con FromURL, orden por id
    OutlinksTotal int    `json:"outlinks_total"`
    InlinksTotal  int    `json:"inlinks_total"`
}

const LinkLimit = 500

type PageWithLinks struct {
    Page  Page
    Links []Link
}

type PageFilter struct {
    Status string // "", "2xx", "3xx", "4xx", "5xx", "error" (status 0 && !blocked), "blocked", "via_nofollow"
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
    ViaNoFollow int         `json:"via_nofollow"`
    ContentTypes map[string]int `json:"content_types"`
    MaxDepth int            `json:"max_depth"`
    AvgDurationMs int64     `json:"avg_duration_ms"` // solo páginas con status > 0
}

type BrokenPage struct {
    Page           Page `json:"page"`            // la página rota (status>=400, o status 0 sin bloqueo)
    ReferrersCount int  `json:"referrers_count"` // nº de enlaces del crawl cuyo to_url == page.url
}

func (s *Store) CreateCrawl(seed string, config json.RawMessage) (*Crawl, error) // status running, started_at now UTC
func (s *Store) SetRobots(crawlID int64, robots string) error
func (s *Store) FinishCrawl(crawlID int64, status, errMsg string) error // status ∈ done|failed|cancelled; finished_at now; ErrNotFound
func (s *Store) GetCrawl(id int64) (*Crawl, error)                     // ErrNotFound
func (s *Store) ListCrawls() ([]Crawl, error)                          // más recientes primero
func (s *Store) DeleteCrawl(id int64) error                            // cascade; ErrNotFound
func (s *Store) AddPage(p Page, links []Link) (int64, error)           // = AddPages con un elemento
func (s *Store) AddPages(crawlID int64, batch []PageWithLinks) ([]int64, error) // UNA transacción: inserta páginas y links, actualiza pages_count, contadores y crawl_content_types; URL duplicada → error y rollback de todo el lote
func (s *Store) ForEachPage(ctx context.Context, crawlID int64, fn func(Page) error) error // streaming por id (spec 008)
func (s *Store) BrokenReferrerCounts(crawlID int64, limit int) ([]BrokenPage, int, error) // spec 008
func (s *Store) RecomputeViaNoFollow(crawlID int64) (cleared int, err error) // pone via_nofollow = 0 en las páginas marcadas que tienen al menos un inlink seguible (link.nofollow = 0 y página origen con nofollow = 0); ajusta count_via_nofollow; una consulta por página marcada (índice links_crawl_to + join con pages); devuelve cuántas se han limpiado
func (s *Store) Checkpoint(mode string) error                          // PRAGMA wal_checkpoint(mode): PASSIVE|FULL|RESTART|TRUNCATE, sobre el pool de escritura
func NewBatchWriter(s *Store, crawlID int64, maxItems int, maxDelay time.Duration) *BatchWriter
func (b *BatchWriter) Add(p Page, links []Link)   // encola; vuelca cuando hay maxItems o han pasado maxDelay desde el primer elemento pendiente
func (b *BatchWriter) Flush() error               // vuelca lo pendiente (síncrono)
func (b *BatchWriter) Close() error               // Flush + para el temporizador; devuelve el primer error de volcado no notificado
func (b *BatchWriter) Err() error                 // último error de volcado (nil si ninguno)
func (s *Store) GetPage(crawlID, pageID int64) (*PageDetail, error)        // ErrNotFound
func (s *Store) FindPageByURL(crawlID int64, url string) (*Page, error)
func (s *Store) ListPages(crawlID int64, f PageFilter) ([]Page, int /*total con filtro*/, error)
func (s *Store) Summarize(crawlID int64) (*Summary, error)             // ErrNotFound si no existe el crawl
func (s *Store) BrokenLinks(crawlID int64, limit, offset int) ([]BrokenPage, int /*total*/, error) // ordenado por url; limit por defecto 100, máx. 1000; ErrNotFound si no existe el crawl
func (s *Store) Referrers(crawlID int64, toURLs []string, perURL int) (map[string][]Link, error) // hasta perURL referrers (con FromURL, orden por id) por cada URL; una sola consulta (window function ROW_NUMBER() OVER (PARTITION BY to_url ORDER BY id))
func (s *Store) Outlinks(crawlID int64) (map[string]int, error)        // opcional: to_url → nº de referrers (no requerido en v1)
```

`FinishCrawl` sobre un crawl que ya no está `running` no cambia nada y no
devuelve error (idempotente), salvo que no exista (ErrNotFound).

## Contadores, lotes y checkpoints (rastreo de 1,1 M de páginas, 2026-09-17)

**Contadores.** `Summarize` no recorre `pages`: lee los `count_*`, `max_depth`,
`duration_sum/duration_n` (media entera, 0 si `duration_n = 0`) de la fila de
`crawls` y `content_types` de `crawl_content_types`. `AddPages` los actualiza
en la misma transacción (`UPDATE crawls SET count_2xx = count_2xx + ?, ...`
y `INSERT INTO crawl_content_types ... ON CONFLICT DO UPDATE SET n = n + excluded.n`,
solo para `content_type <> ''`). `Total` = `pages_count`. `ListPages` usa los
contadores para `total` cuando `Query == ""` (`""` → `pages_count`, `2xx` →
`count_2xx`, ..., `error` → `count_errors`, `blocked` → `count_blocked`); con
`Query` hace `COUNT(*)`.

**Migración.** El esquema es idempotente: `Open` añade con `ALTER TABLE ... ADD
COLUMN` las columnas que falten (comprobando `PRAGMA table_info` de `crawls` y de
`pages`; las nuevas `x_robots_tag`/`via_nofollow`/`count_via_nofollow` se añaden
así a BBDD existentes) y crea
`crawl_content_types` si no existe. Después, para cada crawl con `counters_ok = 0`,
recalcula los contadores y los content types desde `pages` en una transacción y
pone `counters_ok = 1`. Un crawl recién creado nace con `counters_ok = 1`.

**Lotes.** `AddPages` escribe un lote en una transacción. `BatchWriter` es el
único camino recomendado para el rastreo: acumula hasta `maxItems` (server y CLI
usan 100) o `maxDelay` (1 s) y vuelca; un temporizador interno garantiza el
volcado por tiempo sin que llegue otra página. `Add` nunca bloquea al motor más
que el propio volcado; los errores de volcado se guardan (`Err`) y el llamante
los registra. Al terminar o cancelar un rastreo se llama a `Close` **antes** de
`FinishCrawl`. Con lotes de 100, un millón de páginas son 10.000 commits en vez de
un millón: el WAL crece mucho menos entre checkpoints.

**Checkpoints.** `Open` añade al DSN `_pragma=journal_size_limit(67108864)`
(64 MiB: tras un checkpoint el WAL se trunca a ese tamaño) y arranca una goroutine
que cada 60 s ejecuta `Checkpoint("RESTART")` sobre el pool de escritura
(espera, vía `busy_timeout`, a que los lectores en curso terminen; con lecturas
cortas hay hueco y el siguiente escritor reinicia el WAL desde cero).
`FinishCrawl` y `DeleteCrawl` ejecutan `Checkpoint("TRUNCATE")` tras el commit;
un fallo de checkpoint se registra pero no hace fallar la operación. `Close`
para la goroutine y hace un último `TRUNCATE`. Motivo: el 2026-09-17 el WAL
llegó a 45 GB por lectores continuos (checkpoint starvation) y el resumen tardaba
74 s.

## Rendimiento (por qué así)

Un rastreo real de 377.765 páginas dejó la interfaz colgada: `BrokenLinks`
hacía una consulta por página rota (35.000) y devolvía todo sin paginar, y la
única conexión encolaba lecturas tras las escrituras. Reglas:

- `BrokenLinks` es **una sola consulta** paginada: las páginas rotas del crawl
  (`status >= 400` OR (`status = 0` AND `blocked = 0`)) ordenadas por url con
  `LIMIT/OFFSET`, y `ReferrersCount` como subconsulta correlacionada
  `(SELECT COUNT(*) FROM links l WHERE l.crawl_id = p.crawl_id AND l.to_url = p.url)`
  (usa `links_crawl_to`). `total` con un `COUNT(*)` aparte sobre el mismo filtro.
- Los referrers concretos se piden aparte (`Referrers`, acotado por URL) o se
  ven en `GetPage` como inlinks.
- `GetPage` acota outlinks e inlinks a `LinkLimit` y devuelve los totales.
- Ninguna función devuelve listas sin acotar.

## Tests exigidos

Tabla, BBDD real en `t.TempDir()`: esquema idempotente (Open dos veces);
CreateCrawl/GetCrawl/ListCrawls orden; FinishCrawl estados e idempotencia;
running→failed al reabrir; migración: una BBDD creada con el esquema antiguo
(crear las tablas a mano sin las columnas `count_*` ni `crawl_content_types`,
insertar un crawl y páginas) abre bien, gana las columnas y `Summarize` devuelve
los contadores correctos con `counters_ok = 1`; AddPages en lote actualiza
`pages_count`, contadores y content types, y una URL duplicada en el lote
revierte el lote entero; Summarize coincide con un recuento directo sobre
`pages` tras varios lotes; ListPages `total` por contadores para cada `Status`
y por COUNT con `Query`; RecomputeViaNoFollow limpia una página marcada con un inlink normal desde una página sin nofollow, conserva la marca si todos sus inlinks son nofollow o vienen de páginas nofollow, y ajusta `count_via_nofollow`; BatchWriter vuelca por `maxItems`, por `maxDelay`
(test con `maxDelay` de 50 ms y espera), en `Close`, y expone el error de un
volcado fallido (crawl inexistente); Checkpoint("TRUNCATE") deja el fichero
`-wal` a 0 bytes tras escribir; AddPage incrementa `pages_count` y rechaza URL
duplicada; GetPage devuelve out/in links con FromURL, acotados a LinkLimit y con totales (test con más de LinkLimit inlinks: usa una constante pequeña o inserta 501 links); ListPages filtros
(cada valor de Status, Query, Limit/Offset, total); Summarize contadores y
content_types; BrokenLinks paginado con referrers_count y total (una página rota con 3 referrers, otra con 0; limit/offset); Referrers con perURL menor que el nº de enlaces; lectura concurrente: con una transacción de escritura abierta (AddPage a medias o `BEGIN IMMEDIATE` sobre el pool de escritura) una lectura (`GetCrawl`) termina en menos de 1 s; DeleteCrawl cascade; ErrNotFound en
todos los getters.
