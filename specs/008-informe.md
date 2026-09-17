# 008 — Informe por agregados

Un informe de un rastreo que resume, en una pasada, lo que un humano tardaría
horas en sacar con SQL: distribución de códigos por profundidad, idioma y
sección, patrones de redirección, errores agrupados, páginas rotas con más
referrers, títulos duplicados, páginas lentas y muestras aleatorias de cada
grupo problemático. Se genera bajo demanda (nunca por polling), en JSON y en
Markdown, desde la CLI, la API (con descarga) y la web.

## Paquete `report` (`xavi.net/eanbot/report`)

Depende de `store` (tipos y dos métodos de streaming). No depende de `server`
ni de `crawler`. Lógica pura y testeable con datos en memoria.

```go
type Report struct {
    GeneratedAt   time.Time          `json:"generated_at"`
    Crawl         store.Crawl        `json:"crawl"`
    Summary       store.Summary      `json:"summary"`
    ByDepth       []Bucket           `json:"by_depth"`        // clave = profundidad, orden ascendente
    ByStatus      []StatusCount      `json:"by_status"`       // código exacto, orden por n desc; 0 → "ERR" o "BLOQ" según bloqueo
    ByLanguage    []Bucket           `json:"by_language"`     // clave = idioma ("en", "es", "(sin idioma)"), orden por total desc
    BySection     []Bucket           `json:"by_section"`      // clave = sección, top 50 por total + "(otras N secciones)"
    ContentTypes  map[string]int     `json:"content_types"`   // del Summary
    Redirects     []RedirectPattern  `json:"redirects"`       // orden por n desc, top 30
    Errors        []ErrorKind        `json:"errors"`          // orden por n desc, top 20
    TopBroken     []BrokenRanked     `json:"top_broken"`      // top 20 por referrers, de como máximo 5000 páginas rotas
    TopBrokenScanned int             `json:"top_broken_scanned"` // nº de páginas rotas consideradas
    DuplicateTitles []TitleGroup     `json:"duplicate_titles"` // top 20 por n desc (solo 2xx text/html, título no vacío)
    Slowest       []PageRef          `json:"slowest"`         // top 10 por duration_ms (status > 0)
    Largest       []PageRef          `json:"largest"`         // top 10 por size (status > 0)
    Samples       map[string][]PageRef `json:"samples"`       // claves: "3xx","4xx","5xx","error","blocked","noindex"; 10 al azar por clave (reservoir sampling determinista con semilla fija en tests)
}

type Bucket struct {
    Key     string `json:"key"`
    Total   int    `json:"total"`
    S2xx    int    `json:"status_2xx"`
    S3xx    int    `json:"status_3xx"`
    S4xx    int    `json:"status_4xx"`
    S5xx    int    `json:"status_5xx"`
    Errors  int    `json:"errors"`
    Blocked int    `json:"blocked"`
    NoIndex int    `json:"noindex"`
    AvgMs   int64  `json:"avg_ms"`   // media entera sobre status > 0; 0 si no hay
}
type StatusCount struct{ Status string `json:"status"`; N int `json:"n"` }
type RedirectPattern struct {
    Pattern string `json:"pattern"`   // ver "Patrones de redirección"
    N       int    `json:"n"`
    From    string `json:"sample_from"`
    To      string `json:"sample_to"`
}
type ErrorKind struct{ Kind string `json:"kind"`; N int `json:"n"`; Sample string `json:"sample_url"` }
type BrokenRanked struct{ URL string `json:"url"`; Status int `json:"status"`; Error string `json:"error"`; Referrers int `json:"referrers_count"` }
type TitleGroup struct{ Title string `json:"title"`; N int `json:"n"`; Sample string `json:"sample_url"` }
type PageRef struct{ URL string `json:"url"`; Status int `json:"status"`; DurationMs int64 `json:"duration_ms"`; Size int64 `json:"size"`; Depth int `json:"depth"`; Extra string `json:"extra,omitempty"` }

func Build(ctx context.Context, st *store.Store, crawlID int64) (*Report, error) // ErrNotFound si no existe
func Markdown(r *Report) string
```

### Cómo se calcula

Una sola pasada por las páginas del rastreo con `store.ForEachPage` (streaming,
sin cargar todo en memoria), acumulando en mapas; después `store.Summarize`,
`store.GetCrawl` y `store.BrokenReferrerCounts` (acotado). Con 1 M de páginas
debe terminar en pocos segundos y usar memoria acotada: para títulos duplicados
se cuenta por hash de 64 bits del título normalizado (minúsculas, espacios
colapsados) guardando el texto y una URL de muestra solo para los grupos que
llegan a 2. `ctx` cancela la pasada.

- **Idioma**: primer segmento del path si tiene exactamente 2 letras ASCII
  (sin distinguir mayúsculas; se normaliza a minúsculas); si no, `(sin idioma)`.
- **Sección**: el segmento siguiente al idioma (o el primero si no hay idioma);
  path vacío o `/` → `(raíz)`. Se decodifica el percent-encoding para agrupar
  `w%C3%B6hner` con `wöhner`.
- **Errores** (`status == 0` y no bloqueada): tipo por coincidencia, en este
  orden: `robots.txt` (empieza por `robots.txt:`), `timeout` (`Client.Timeout`
  o `deadline exceeded`), `conexión rechazada` (`connection refused`),
  `tls` (`tls:` o `x509`), `conexión cerrada` (`EOF` o `reset by peer`),
  `dns` (`no such host`), `cuerpo` (`truncated`/`body`), y si no `otro:` +
  primeros 60 caracteres del error con la URL eliminada.

### Patrones de redirección

Para cada página 3xx con `redirect_to` no vacío (solo si origen y destino
comparten host tras quitar `www.`; si no, patrón `otro host`):

| Patrón | Condición |
|---|---|
| `añade barra final` | `to == from + "/"` |
| `quita barra final` | `to + "/" == from` |
| `http→https` | igual salvo el esquema |
| `cambia mayúsculas` | `lower(from) == lower(to)`, distintos |
| `quita query` | `to == from` sin la query |
| `segmento N: a → b` | mismo nº de segmentos y solo difiere el segmento N (1-based); `a`/`b` son los valores; se agrupa por (N, a, b) |
| `añade prefijo /x` | `to` == `from` con un segmento insertado al principio (p. ej. `/` → `/en`) |
| `otra ruta` | resto |

### Markdown

`Markdown(r)` produce un documento con: título (`# Informe del rastreo #id —
semilla`), línea de estado/fechas/config resumida, tabla de resumen, y una
sección `##` por bloque en este orden: Códigos, Por profundidad, Por idioma,
Por sección (top 50), Tipos de contenido, Redirecciones, Errores, Páginas
rotas con más referrers, Títulos duplicados, Páginas más lentas, Páginas más
grandes, Muestras. Tablas Markdown con cabecera; secciones vacías dicen
`Sin datos.` Números con separador de miles (`.`), textos en español, URLs
tal cual (sin acortar, sin enlaces).

## Store (añadido en 003)

```go
func (s *Store) ForEachPage(ctx context.Context, crawlID int64, fn func(Page) error) error // orden por id; corta si fn devuelve error o ctx se cancela
func (s *Store) BrokenReferrerCounts(crawlID int64, limit int) ([]BrokenPage, int, error) // hasta limit páginas rotas (orden por url) con referrers_count; el int es el total de rotas
```

## API (añadido en 004)

| Método y ruta | Respuesta |
|---|---|
| `GET /api/crawls/{id}/report` | `200 {"report": Report}` |
| `GET /api/crawls/{id}/report?format=md` | `200` `text/markdown; charset=utf-8`, cuerpo = `Markdown(r)` |
| `...&download=1` | añade `Content-Disposition: attachment; filename="eanbot-rastreo-{id}.md"` (o `.json`) |

`format` distinto de `json`/`md` → `400 {"errors":["format no válido"]}`.
El informe no se cachea; el handler usa `r.Context()` para cancelar si el
cliente se va.

## CLI (añadido en 005)

```
eanbot report <crawl-id> [-db eanbot.db] [-json] [-o fichero]
```
Markdown por defecto a stdout; `-json` el objeto `Report`; `-o` escribe al
fichero (0644) y no imprime nada salvo `informe escrito en <fichero>` en
stderr. Id inexistente → `error: rastreo no encontrado`, código 1.

## Web (añadido en 004)

Pestaña **Informe** en el detalle del rastreo, junto a Páginas y Enlaces
rotos. Contenido inicial: botón «Generar informe» (deshabilitado mientras el
rastreo está `running`, con nota «disponible al terminar»; un informe de un
rastreo en marcha es parcial pero se permite si el usuario pulsa «Generar
igualmente»). Al generar: indicador de carga («generando, puede tardar unos
segundos»), después las secciones del informe renderizadas como tablas (mismo
orden que el Markdown, secciones vacías ocultas, secciones colapsables con
`<details>`, abiertas por defecto Códigos, Redirecciones y Errores), y dos
botones «Descargar Markdown» y «Descargar JSON» que son enlaces `<a href
download>` a `/api/crawls/{id}/report?format=md&download=1` y
`...?format=json&download=1`. Las URLs de las tablas enlazan a su detalle de
página cuando se conoce el `page_id`; si no, texto plano. Error de red →
«la petición ha fallado»; cambiar de rastreo limpia el informe.

## Tests exigidos

- `report`: `Build` sobre un store en `t.TempDir()` sembrado con páginas que
  cubren cada bucket (2 idiomas, 3 secciones, profundidades 0–3, 3xx de cada
  patrón incluido `segmento 2: a → b` con 3 páginas, errores de 4 tipos, 2
  títulos duplicados, bloqueadas, noindex) y links para referrers: comprobar
  cada campo; `Samples` con semilla determinista (variable de paquete para el
  `rand.Source`, o inyección) y como máximo 10; cancelación por ctx; crawl
  inexistente → `ErrNotFound`. `Markdown`: contiene todas las cabeceras `##`,
  `Sin datos.` en secciones vacías, miles con punto; snapshot fijo en
  `report/testdata/report.md` comparado byte a byte.
- `store`: `ForEachPage` orden e interrupción; `BrokenReferrerCounts` límite y
  total.
- `server`: JSON, `format=md` con Content-Type, `download=1` con
  Content-Disposition, `format` inválido → 400, id inexistente → 404.
- `cmd`: `report` Markdown a stdout, `-json` parseable, `-o` escribe fichero,
  id inexistente → 1.
