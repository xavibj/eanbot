# 006 — Fases de implementación

Cada fase la ejecuta un subagente con propiedad exclusiva de ficheros. Orden
por dependencias. `go.mod`/`go.sum` no se tocan (ya fijados por el orquestador).

| Fase | Ficheros (SOLO estos) | Depende de | Spec |
|---|---|---|---|
| A1 crawler | `crawler/**` | — | 002 |
| A2 store | `store/**` | — | 003 |
| B server | `server/**` (Go) + `server/static/index.html` placeholder | A1, A2 | 004 |
| C1 cli | `cmd/eanbot/**` | B | 005 |
| C2 web | `web/**` y la salida `server/static/**` (build) | B | 004 (frontend) |
| D verificación | orquestador: binario real, rastreo de xavibolivar.xavi.net, curl, navegador | C1, C2 | 000 |

A1 y A2 en paralelo; C1 y C2 en paralelo.

Definición de terminado por fase: tests escritos antes (rojo confirmado),
`go test ./... && go vet ./... && gofmt -l .` limpios en todo el repo, informe
compacto (ficheros, decisiones, desviaciones de spec, resultado de la suite).
Si una decisión de implementación contradice una spec, la spec se actualiza en
el mismo cambio.
