# 004 — Paquete `server`: API REST, Manager y frontend

Paquete `xavi.net/eanbot/server`. Depende de `store` y `crawler`.

## Constructor

```go
type Server struct{ ... }
func New(st *store.Store, opts Options) *Server
func (s *Server) Handler() http.Handler
func (s *Server) Shutdown(ctx context.Context) error  // cancela rastreos en marcha y espera

type Options struct {
    NewFetcher func(cfg crawler.Config) crawler.Fetcher // nil → crawler.NewHTTPFetcher(...)
}
```

`NewFetcher` inyectable permite tests sin red (Fetcher falso).

## Manager (rastreos en segundo plano)

Dentro de `server`: `Manager` con `Start(cfg crawler.Config) (crawlID, error)`,
`Cancel(id) bool`, `Running() []int64`. Por cada rastreo: `store.CreateCrawl`
(config serializada como el objeto `config` de abajo), goroutine que llama a
`crawler.Run` con un `context.WithCancel`, un `Sink` que hace
`store.BatchWriter` (`NewBatchWriter(st, id, 100, time.Second)`, convirtiendo tipos; los errores de volcado se registran en el log), `Close()` del writer al terminar `crawler.Run` y, después, `store.FinishCrawl` con
`done` (sin error), `cancelled` (ctx cancelado) o `failed` (otro error, texto en
`error`). `Stats.RobotsTxt` se guarda con `SetRobots` en cuanto se conoce
(el Sink puede recibirlo tras la primera página: el Manager lo guarda al
final de `Run` con el valor de Stats; suficiente).

## Contrato JSON

Todas las respuestas `/api/` llevan `Content-Type: application/json` y salto
de línea final. Errores de validación: `400 {"errors":["...", "..."]}` (todos a
la vez; las cadenas de `crawler.Config.Validate`). JSON malformado → `400
{"errors":["json malformado"]}`. Método no permitido → `405` con cabecera
`Allow`. Recurso inexistente → `404 {"errors":["no encontrado"]}`. Ruta
`/api/*` desconocida → `404` JSON. Error interno → `500 {"errors":["error
interno"]}`.

### Objeto `config` (petición y almacenado)

```json
{
  "seed": "https://xavibolivar.xavi.net",
  "max_pages": 500, "max_depth": 10, "concurrency": 4, "delay_ms": 500,
  "timeout_ms": 15000, "max_body_bytes": 2097152,
  "user_agent": "EANBot/0.1 (+https://xavi.net)",
  "include_subdomains": false, "ignore_robots": false, "use_sitemaps": true,
  "headers": {"x-crawler-token": "XXXXXXXX"},
  "origin": "203.0.113.10", "insecure_tls": false,
  "follow_nofollow": false
}
```

`follow_nofollow` (bool, por defecto `false`): seguir también los enlaces
`rel=nofollow` y los de páginas con `nofollow` (meta o `X-Robots-Tag`). Los
enlaces se guardan igual con `nofollow: true`; las páginas alcanzadas solo así
llevan `via_nofollow: true`.

`origin` (`ip` o `ip:puerto`, opcional) conecta directamente al servidor de
origen para el host de la semilla, saltando Cloudflare, manteniendo `Host` y
SNI (ver spec 002). `insecure_tls` (bool, opcional, por defecto `false`)
desactiva la verificación del certificado. El servidor rellena
`HTTPFetcher.OriginHost` con el host de `crawler.Normalize(seed)`. Ambos se
almacenan siempre en el config (`""` / `false` si no vienen). Los errores de
validación son los de `crawler.Config.Validate`.

`headers` es un objeto opcional nombre → valor (cadenas); se envía en todas
las peticiones del rastreo (útil para que un WAF como Cloudflare no bloquee
al bot). Ausente o `null` → sin cabeceras extra (se almacena como `{}`).
Los errores de validación de cabeceras son los de `crawler.Config.Validate`.
Campos ausentes o 0 → valor por defecto (`WithDefaults`), **excepto
`delay_ms`**: `crawler.WithDefaults` respeta `Delay == 0` como valor explícito
(sin cortesía), así que el servidor decodifica `delay_ms` como puntero (`*int`)
y aplica 500 solo cuando el campo no viene en el JSON; `"delay_ms": 0`
significa sin retardo. En `POST` solo `seed` es obligatorio. El objeto
almacenado es el resultante tras defaults.

### Endpoints

| Método y ruta | Respuesta |
|---|---|
| `GET /api/healthz` | `200 {"ok":true}` |
| `POST /api/crawls` body `config` | `201` objeto crawl (status `running`) |
| `GET /api/crawls` | `200 {"crawls":[crawl...]}` (más recientes primero) |
| `GET /api/crawls/{id}` | `200 {"crawl":crawl, "summary":summary, "running":bool}` |
| `POST /api/crawls/{id}/cancel` | `200 {"crawl":crawl}`; si no estaba en marcha, 200 igualmente |
| `DELETE /api/crawls/{id}` | `204`; si está en marcha se cancela antes |
| `GET /api/crawls/{id}/pages?status=&q=&limit=&offset=` | `200 {"pages":[page...], "total":N, "limit":L, "offset":O}` |
| `GET /api/crawls/{id}/pages/{page_id}` | `200 {"page":page, "outlinks":[link...], "inlinks":[link...], "outlinks_total":N, "inlinks_total":N}` (listas acotadas a 500) |
| `GET /api/crawls/{id}/report[?format=json\|md][&download=1]` | informe por agregados, ver `specs/008-informe.md` |
| `GET /api/crawls/{id}/broken?limit=&offset=` | `200 {"broken":[{"page":page,"referrers_count":N}...], "total":T, "limit":L, "offset":O}` (limit por defecto 100, máx. 1000) |

`crawl`, `summary`, `page`, `link` son los structs de `store` serializados
(campos `snake_case` de 003). `{id}` no numérico → 404. `limit` fuera de
[1,1000] → se acota; `status` con valor no listado en 003 → `400
{"errors":["status no válido"]}`.

## Estáticos

`//go:embed static` sirve `server/static/` (salida de Vite). `GET /` →
`index.html`. Cualquier ruta que no empiece por `/api/` y no corresponda a un
fichero → `index.html` (fallback SPA) con `200`. Assets con `Content-Type`
correcto (`.js` → `text/javascript`, `.css` → `text/css`). Cabecera
`Cache-Control: no-cache` en `index.html`; `max-age=31536000, immutable` en
`/assets/*` (nombres con hash).

Hasta que exista el build de Vite, `server/static/index.html` es un
placeholder mínimo (`<div id="app"></div>` y texto «eanbot») para que
`go:embed` compile.

## Frontend (`web/`, Vite + Vue 3)

- `web/package.json` con `vue` y `@vitejs/plugin-vue`, `vite`; scripts
  `dev`, `build`. `vite.config.js`: `build.outDir = '../server/static'`,
  `emptyOutDir: true`, `server.proxy['/api'] = 'http://localhost:8345'`.
- Sin router: vista controlada por `location.hash` (`#/`, `#/new`,
  `#/crawls/{id}`, `#/crawls/{id}/pages/{pageId}`), para que recargar
  mantenga la vista.
- Textos en español. Diseño limpio, funcional, que funcione a 390 px de ancho
  (tablas dentro de contenedor con `overflow-x:auto`). CSS propio, sin
  frameworks.
- Vistas:
  1. **Rastreos** (`#/`): tabla (semilla, estado, páginas, inicio, duración)
     con botón «Nuevo rastreo»; auto-refresco cada 2 s mientras haya alguno
     `running`. Acciones: abrir, cancelar (si running), borrar (confirm).
  2. **Nuevo rastreo** (`#/new`): formulario con semilla (obligatoria,
     valor inicial `https://xavibolivar.xavi.net`), máx. páginas, profundidad,
     concurrencia, retardo ms, incluir subdominios, ignorar robots, usar
     sitemaps, y un `textarea` «Cabeceras adicionales» (una por línea,
     formato `Nombre: valor`; líneas vacías ignoradas; una línea sin `:` se
     rechaza en cliente con el mensaje «cabecera sin “:” en la línea N»).
     Se envía como objeto `headers`. Campo «IP del origen (saltar
     Cloudflare)». Checkbox «Seguir enlaces nofollow» (`follow_nofollow`, siempre
     enviado; ayuda: «Googlebot los trata como pista; para auditar todo lo
     enlazado»). Campo «IP del origen (saltar
     Cloudflare)» (`type="text"`, placeholder `203.0.113.10`, ayuda: «ip o
     ip:puerto; la cabecera Host y el SNI siguen siendo los del sitio») que se
     envía como `origin` solo si no está vacío, y checkbox «No verificar el
     certificado TLS» (`insecure_tls`, siempre enviado; ayuda: «solo para
     certificados Origin CA de Cloudflare o propios»). Inputs numéricos como `type="text" inputmode="numeric"` con
     coerción defensiva. Errores `400` se muestran en lista bajo el
     formulario; controles deshabilitados durante la petición; error de red
     → «la petición ha fallado». Al crear, navega a `#/crawls/{id}`.
  3. **Detalle de rastreo** (`#/crawls/{id}`): cabecera (semilla, estado,
     botón cancelar si running), tarjetas de resumen (total, 2xx, 3xx, 4xx,
     5xx, errores, bloqueadas, noindex, y «solo vía nofollow» si el rastreo
     tiene `follow_nofollow`), filtro de estado con la opción «Solo vía
     nofollow» (`status=via_nofollow`), pestañas «Páginas», «Enlaces rotos» e «Informe» (esta última según
     `specs/008-informe.md`). Páginas: filtro por estado (select), búsqueda (input con
     debounce 300 ms), tabla (código, URL, título, tipo, profundidad, ms) con
     paginación (100 por página); clic en fila → `#/crawls/{id}/pages/{pid}`.
     Enlaces rotos: tabla paginada (100 por página, misma paginación que
     Páginas) con código o error, URL y nº de referrers; clic en la fila →
     detalle de la página, cuya lista de enlaces entrantes son los referrers.
     Auto-refresco cada 2 s mientras `running`. En la línea de config se
     muestra `origen: <ip>` si `config.origin` no está vacío y `tls sin
     verificar` si `config.insecure_tls`.
  4. **Detalle de página**: todos los campos de `page` en una lista de
     definición (incluidos `x_robots_tag` y `via_nofollow`, este último como
     «Sí, descubierta solo por enlaces nofollow» / «No»); enlaces salientes (URL, texto, nofollow, en ámbito) y
     entrantes (URL origen, texto), cada lista con su cabecera «N de total»
     y, si `total > N`, una nota «mostrando los primeros N».
- Cualquier cambio de filtro limpia resultados obsoletos antes de pedir.

## Tests exigidos (`httptest` contra `Handler()`, Fetcher falso)

healthz; el summary del crawl terminado coincide con las páginas servidas por el Fetcher falso aunque el rastreo termine antes de 1 s (el Close del BatchWriter vuelca lo pendiente antes de FinishCrawl); POST con `headers` → el Fetcher falso recibe la cabecera en cada
petición (el `NewFetcher` inyectado recibe la `Config` con `Headers`) y el
config almacenado la incluye; POST con cabecera inválida → 400; POST con `follow_nofollow` → la Config del Fetcher inyectado lo lleva y el config almacenado también (`false` si no viene); `/pages?status=via_nofollow`; POST con `origin` e `insecure_tls` → el
Fetcher inyectado recibe la Config con ambos y el config almacenado los
incluye (y `"origin":"","insecure_tls":false` cuando no vienen); POST con
`origin` inválido → 400 `origin no válido: "..."`; POST válido → 201 y crawl en store, y tras esperar a que termine
(polling a GET hasta `running == false`, con timeout) el summary coincide con
lo que sirvió el Fetcher falso; POST inválido → 400 con TODOS los errores;
JSON malformado; 405 con Allow; 404 JSON en id inexistente, id no numérico,
ruta desconocida; list; pages con filtros, paginación y status inválido;
page detail con out/in links y totales; broken paginado (total, limit/offset, referrers_count); cancel (Fetcher falso lento que
respeta ctx) → status `cancelled`; delete → 204 y luego 404; estáticos: `/`
200 `text/html` con `<div id="app"`, fallback SPA, `Content-Type` de assets
referenciados por `index.html`, `Cache-Control`.
