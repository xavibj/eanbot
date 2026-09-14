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
    Delay             time.Duration // >=0; por defecto 500ms (0 es un valor
                                     // explícito válido: "sin cortesía",
                                     // como los bools, WithDefaults no lo toca)
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
func (c Config) WithDefaults() Config   // rellena ceros con Defaults() (no toca bools ni Delay)

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
    RobotsTxt                string // robots.txt del host final (ver "Motor")
    Sitemaps                 int    // URLs descubiertas vía sitemaps
    FinalHost                string // host efectivo del ámbito (ver "Motor", cambio de ámbito de la semilla)
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
2. Normaliza la semilla; `seedHost` = host normalizado (provisional: puede
   cambiar, ver punto 4 — "Redirecciones de la semilla y cambio de ámbito").
3. robots del host de la semilla (ver arriba). `delay = max(cfg.Delay,
   robots.CrawlDelay)`.
4. **Redirecciones de la semilla y cambio de ámbito.** Se obtiene la semilla
   (profundidad 0) directamente (sin pasar por la frontier general) y se
   sigue su posible cadena de redirecciones 3xx de profundidad 0:
   - Cada salto de la cadena (incluida la semilla) respeta robots.txt y
     `MaxPages` con las mismas reglas que el resto del motor (punto 7 y 8);
     cada 3xx intermedio se entrega al `Sink` como `Page` normal y cuenta en
     `Fetched`.
   - Si el `Location` resuelto está en el mismo ámbito (`SameSite` contra el
     `seedHost` actual), se sigue sin más: se obtiene esa URL a
     profundidad 0 y se repite el proceso.
   - Si el `Location` resuelto NO está en el mismo ámbito, el motor **adopta
     ese host como `seedHost`** (el ámbito pasa a ser el nuevo host, con las
     mismas reglas de `www.`/subdominios de `SameSite`), vuelve a obtener y
     aplicar el robots.txt del nuevo host (mismas reglas 2xx/4xx/5xx que en
     el punto 3; con `IgnoreRobots` no se pide para ningún host) y continúa
     la cadena a profundidad 0 desde ese destino. Máximo **5 saltos de
     cambio de host**; alcanzado el límite, esa 3xx se registra igual (ya
     contada arriba) pero no se sigue.
   - Un `Location` que resuelve a una URL ya vista en esta cadena (bucle de
     redirecciones) detiene la cadena sin más.
   - La cadena termina en la primera respuesta no-3xx (o 3xx sin `Location`
     utilizable), en un error del Fetcher, en un bloqueo por robots.txt, o
     al agotar el límite de saltos. Esa página final es la que aporta los
     enlaces salientes a la frontier (con las mismas reglas del punto 6);
     `seedHost`, el robots.txt aplicado y `delay` quedan fijados en lo que
     resulte de este proceso. `Stats.FinalHost` refleja ese host efectivo;
     `Stats.RobotsTxt` refleja el robots.txt del host final.
   - Redirecciones de profundidad > 0 a otro host siguen sin seguirse (ver
     punto 6): esta lógica de cambio de ámbito sólo aplica a la profundidad 0
     derivada de la semilla.
5. Sitemaps (ver arriba), ya con el host, robots y ámbito finales del
   punto 4 (si la semilla no redirige, esto ocurre igual que antes, justo
   tras resolverla).
6. Bucle: `Concurrency` workers sobre el resto de la frontier (profundidad
   ≥ 1, más lo que el punto 4 haya dejado pendiente); un limitador global
   asegura que entre dos inicios de petición pasan al menos `delay` (un
   `time.Ticker`/último inicio con mutex). El coordinador saca de la
   frontier mientras `fetched < MaxPages`. Cada resultado: `Sink.Page(p)`;
   para cada `Link` en ámbito, no nofollow, y `p.NoFollow == false`, con
   `p.Depth+1 <= MaxDepth` → Push(depth+1). `RedirectTo` en ámbito →
   Push(misma profundidad). `Canonical` no se encola.
7. URL prohibida por robots → `Page{Blocked:true, Status:0}` al Sink, no cuenta
   en `Fetched`, cuenta en `Blocked`.
8. Error del Fetcher → `Page{Error: err.Error()}`; cuenta en Fetched y Errors.
9. Termina: frontier vacía y ningún worker ocupado, o `Fetched == MaxPages`
   (las URLs en cola restantes se cuentan en `Stats.Queued`), o ctx cancelado
   (se espera a los workers en vuelo, se devuelve `Stats` parciales y el error).
10. Con `Concurrency == 1` el orden de páginas al Sink es determinista (BFS),
    incluyendo la cadena de redirecciones de la semilla del punto 4, que
    siempre se resuelve antes de arrancar los workers.

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
- Run — redirecciones de la semilla (punto 4 de "Motor"): la semilla
  redirige a otro host y el ámbito pasa a ser ese host; cadena de dos saltos
  de host; límite de 5 saltos (el 6º se registra y no se sigue);
  redirección a otro host en profundidad 1, que sigue sin seguirse;
  robots.txt del nuevo host obtenido y aplicado tras el cambio de ámbito
  (`Stats.RobotsTxt`/`Stats.FinalHost` reflejan el host final); sitemaps
  resueltos con el host final tras la cadena de la semilla.
- HTTPFetcher con `httptest.Server`: User-Agent enviado, no sigue 301,
  Truncated con body > MaxBodyBytes, timeout.
