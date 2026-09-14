# 002 — Paquete `crawler` (dominio)

Paquete `xavi.net/eanbot/crawler`. Sin SQLite, sin `cmd`, sin `server`. La única
I/O es a través de la interfaz `Fetcher` (inyectable) y de un `Sink`.

## Tipos públicos

```go
type Config struct {
    Seed              string        // URL semilla (obligatoria, http/https)
    MaxPages          int           // >0; por defecto 500
    MaxDepth          int           // >=0; por defecto 10
    Concurrency       int           // >0; por defecto 4
    Delay             time.Duration // >=0; por defecto 500ms
    Timeout           time.Duration // por petición; por defecto 15s
    MaxBodyBytes      int64         // por defecto 2 MiB
    UserAgent         string        // por defecto "EANBot/0.1 (+https://xavi.net)"
    RobotsToken       string        // por defecto "eanbot"
    IncludeSubdomains bool
    IgnoreRobots      bool
    UseSitemaps       bool          // por defecto true (ver Defaults)
}

func Defaults() Config                  // los valores de specs/001
func (c Config) Validate() []string     // TODAS las violaciones, texto en español (ver abajo)
func (c Config) WithDefaults() Config   // rellena ceros con Defaults() (no toca bools)

type Response struct {
    Status      int
    ContentType string        // cabecera cruda
    Location    string        // cabecera Location si 3xx
    Body        []byte        // como máximo MaxBodyBytes
    Truncated   bool          // body cortado por MaxBodyBytes
    Size        int64         // bytes leídos (== len(Body))
    Duration    time.Duration
}

type Fetcher interface {
    Fetch(ctx context.Context, url string) (*Response, error)
}

// HTTPFetcher implementa Fetcher con net/http. NO sigue redirecciones
// (CheckRedirect devuelve http.ErrUseLastResponse). Envía User-Agent y
// Accept: text/html,application/xhtml+xml,*/*;q=0.8. Lee como máximo
// MaxBodyBytes+1 para detectar Truncated.
func NewHTTPFetcher(userAgent string, timeout time.Duration, maxBody int64) *HTTPFetcher

type Link struct {
    URL      string // absoluta y normalizada
    Text     string // texto del ancla, recortado (máx. 200 runas)
    NoFollow bool
    InScope  bool
}

type Page struct {
    URL         string
    Depth       int
    Status      int           // 0 si no se hizo petición (bloqueada o error de red)
    ContentType string        // solo el media type, en minúsculas, sin parámetros ("text/html")
    Size        int64
    Duration    time.Duration
    Title       string
    Description string
    Canonical   string        // absoluta normalizada, "" si no hay
    MetaRobots  string        // contenido crudo de <meta name="robots">
    NoIndex     bool
    NoFollow    bool          // meta robots nofollow
    H1          string
    RedirectTo  string        // absoluta normalizada si 3xx con Location
    Error       string        // "" si ok; texto del error de red/lectura
    Blocked     bool          // prohibida por robots.txt (no se pidió)
    Links       []Link        // salientes (vacío si no es HTML)
    FetchedAt   time.Time
}

type Sink interface {
    Page(p Page)                 // llamada por cada URL procesada (fetch, bloqueo o error)
}

type Stats struct {
    Fetched, Blocked, Errors int // Fetched cuenta cualquier petición realizada (cualquier código)
    Queued                   int // URLs que quedaron sin visitar por límites
    RobotsTxt                string
    Sitemaps                 int // URLs descubiertas vía sitemaps
}

// Run ejecuta el rastreo completo y devuelve estadísticas. Termina cuando
// la cola se vacía, se alcanza MaxPages, o ctx se cancela (devuelve ctx.Err()
// envuelto). Sink recibe las páginas en el orden en que terminan.
func Run(ctx context.Context, cfg Config, f Fetcher, s Sink) (Stats, error)
```

## Normalización de URL

`func Normalize(raw string, base *url.URL) (string, error)`:

1. `strings.TrimSpace`; vacío → error.
2. Resolver contra `base` si no es absoluta (`base.ResolveReference`).
3. Esquema en minúsculas; solo `http`/`https`, si no → error.
4. Host en minúsculas; quitar puerto por defecto (`:80` http, `:443` https).
5. Fragmento eliminado. Query vacía (`?`) eliminada; el resto de la query se
   conserva tal cual (sin reordenar).
6. Path vacío → `/`. Los `..`/`.` los resuelve `ResolveReference`.
7. Sin userinfo (si lo hay → error).

`func SameSite(seedHost, host string, includeSubdomains bool) bool`: compara
tras quitar prefijo `www.` de ambos; con `includeSubdomains`, también
`strings.HasSuffix(host, "."+seedHost)`.

## robots.txt

```go
type Robots struct{ ... }
func ParseRobots(r io.Reader) *Robots
func (r *Robots) Allowed(token, path string) bool   // path incluye query si la hay
func (r *Robots) CrawlDelay(token string) time.Duration // 0 si no hay
func (r *Robots) Sitemaps() []string
func AllowAll() *Robots
func DisallowAll() *Robots
```

- Líneas `campo: valor`, campo sin distinguir mayúsculas, `#` comentario.
- Grupos: uno o más `User-agent` seguidos de reglas. Se elige el grupo con
  `User-agent` que contenga el token (sin mayúsculas); si no, el grupo `*`; si
  no hay ninguno, todo permitido.
- `Disallow:` vacío = permitir todo. Reglas con `*` (cualquier secuencia) y `$`
  (fin). Gana la regla de coincidencia más larga (longitud del patrón); en
  empate gana `Allow`.
- `Crawl-delay` en segundos (decimal permitido).
- `Sitemap:` es global (fuera de grupos).

Obtención (en `Run`): GET `<scheme>://<host>/robots.txt` con el Fetcher.
2xx → parse; 4xx → `AllowAll`; 5xx o error → `DisallowAll` (el rastreo
termina con la semilla marcada `Blocked`). Con `IgnoreRobots`, no se pide
robots (RobotsTxt vacío) y se permite todo.

## Extracción de HTML

`func ParseHTML(body []byte, pageURL *url.URL, seedHost string, includeSub bool) (Page, error)`
rellena Title, Description, Canonical, MetaRobots/NoIndex/NoFollow, H1 y Links:

- `<base href>` (el primero) cambia la base de resolución.
- Solo `<a href>`. Se descartan `mailto:`, `tel:`, `javascript:`, `data:` y
  hrefs vacíos o solo fragmento. Dedupe por URL normalizada (se conserva el
  primer texto).
- `Title`: texto de `<title>` con espacios colapsados. `H1`: primer `<h1>`.
- `<meta name="description">`, `<meta name="robots">` (name sin mayúsculas;
  `noindex`/`nofollow` como tokens separados por coma o espacio).
- `<link rel="canonical" href>` (primer canonical).
- `rel` del `<a>` contiene `nofollow` → `Link.NoFollow`.
- Texto del ancla: texto de nodos hijos, espacios colapsados, máx. 200 runas.

`ContentType` del `Page` sale de `mime.ParseMediaType` de la cabecera; se
parsea HTML solo si es `text/html` o `application/xhtml+xml`.

## Sitemaps

Si `UseSitemaps` y no `IgnoreRobots`: por cada `Sitemap:` de robots se hace
GET; si el body empieza por gzip (magia `1f 8b`) o la URL acaba en `.gz`, se
descomprime. `<sitemapindex>` → se recorren los `<loc>` hijos (máx. profundidad
de índice 2). `<urlset>` → cada `<loc>` en ámbito se encola con profundidad 1
(si no estaba ya). Límite total 10 000 URLs de sitemap. Las URLs de sitemap
NO se registran como Page hasta que se visitan. Errores de sitemap se ignoran
(no rompen el rastreo).

## Frontier

```go
type Frontier struct{ ... }
func NewFrontier() *Frontier
func (f *Frontier) Push(url string, depth int) bool  // false si ya vista (encolada o visitada)
func (f *Frontier) Pop() (url string, depth int, ok bool)
func (f *Frontier) Len() int
```
FIFO estricta (BFS), dedupe por URL exacta normalizada. No es concurrente por
sí misma; el motor la protege con un mutex.

## Motor (`Run`)

1. `cfg = cfg.WithDefaults()`; si `Validate()` devuelve errores → `error` con
   ellos unidos por `; `.
2. Normaliza la semilla; `seedHost` = host normalizado. Push(seed, 0).
3. robots (ver arriba). `delay = max(cfg.Delay, robots.CrawlDelay)`.
4. Sitemaps (ver arriba).
5. Bucle: `Concurrency` workers; un limitador global asegura que entre dos
   inicios de petición pasan al menos `delay` (un `time.Ticker`/último inicio
   con mutex). El coordinador saca de la frontier mientras `fetched < MaxPages`.
   Cada resultado: `Sink.Page(p)`; para cada `Link` en ámbito, no nofollow, y
   `p.NoFollow == false`, con `p.Depth+1 <= MaxDepth` → Push(depth+1).
   `RedirectTo` en ámbito → Push(misma profundidad). `Canonical` no se encola.
6. URL prohibida por robots → `Page{Blocked:true, Status:0}` al Sink, no cuenta
   en `Fetched`, cuenta en `Blocked`.
7. Error del Fetcher → `Page{Error: err.Error()}`; cuenta en Fetched y Errors.
8. Termina: frontier vacía y ningún worker ocupado, o `Fetched == MaxPages`
   (las URLs en cola restantes se cuentan en `Stats.Queued`), o ctx cancelado
   (se espera a los workers en vuelo, se devuelve `Stats` parciales y el error).
9. Con `Concurrency == 1` el orden de páginas al Sink es determinista (BFS).

## Mensajes de `Validate()` (exactos)

- `la semilla es obligatoria`
- `la semilla debe ser una URL http o https absoluta`
- `max_pages debe ser mayor que 0`
- `max_depth no puede ser negativo`
- `concurrency debe ser mayor que 0`
- `delay_ms no puede ser negativo`

## Tests exigidos (mínimo)

- Normalize: tabla con relativos, mayúsculas, puertos por defecto, fragmento,
  `?` vacío, `..`, esquemas rechazados, userinfo.
- SameSite: www, subdominios con y sin flag, host distinto.
- ParseRobots: grupos por token y `*`, longest-match, Allow en empate, `*`/`$`,
  Crawl-delay, Sitemap, Disallow vacío, fichero vacío.
- ParseHTML: base href, canonical, meta robots, nofollow, esquemas descartados,
  dedupe, texto de ancla, ausencia de title.
- Frontier: dedupe y orden FIFO.
- Run con Fetcher falso (mapa URL→Response, sin red) y Concurrency 1:
  BFS determinista, MaxPages, MaxDepth, bloqueo por robots (4xx, 5xx, grupo
  específico), redirección en/fuera de ámbito, nofollow, sitemap con índice
  y gzip, cancelación por contexto, no-HTML sin enlaces.
- HTTPFetcher con `httptest.Server`: User-Agent enviado, no sigue 301,
  Truncated con body > MaxBodyBytes, timeout.
