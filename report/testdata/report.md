# Informe del rastreo #1 — https://example.com/

Estado: done · Inicio: 2026-09-17 09:00:00 UTC · Fin: 2026-09-17 09:30:00 UTC · Duración: 30m0s
Generado: 2026-09-17 10:00:00 UTC
Configuración: máx. páginas 500 · máx. profundidad 10 · concurrencia 4 · retardo 500ms · timeout 15s · subdominios: no · robots: sí · UA: eanbot/0.1

| Métrica | Valor |
|---|---|
| Páginas | 27 |
| 2xx | 7 |
| 3xx | 11 |
| 4xx | 2 |
| 5xx | 1 |
| Errores | 5 |
| Bloqueadas | 1 |
| noindex | 1 |
| Profundidad máxima | 3 |
| Tiempo medio de respuesta | 96 ms |

## Códigos

| Código | Páginas |
|---|---|
| 301 | 9 |
| 200 | 7 |
| ERR | 5 |
| 302 | 2 |
| 404 | 1 |
| 410 | 1 |
| 500 | 1 |
| BLOQ | 1 |

## Por profundidad

| Profundidad | Páginas | 2xx | 3xx | 4xx | 5xx | Errores | Bloq. | noindex | Media (ms) |
|---|---|---|---|---|---|---|---|---|---|
| 0 | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 0 | 100 |
| 1 | 13 | 2 | 10 | 0 | 0 | 0 | 1 | 0 | 54 |
| 2 | 11 | 2 | 1 | 2 | 1 | 5 | 0 | 1 | 170 |
| 3 | 2 | 2 | 0 | 0 | 0 | 0 | 0 | 0 | 125 |

## Por idioma

| Idioma | Páginas | 2xx | 3xx | 4xx | 5xx | Errores | Bloq. | noindex | Media (ms) |
|---|---|---|---|---|---|---|---|---|---|
| (sin idioma) | 14 | 2 | 3 | 2 | 1 | 5 | 1 | 0 | 40 |
| en | 9 | 2 | 7 | 0 | 0 | 0 | 0 | 0 | 68 |
| es | 4 | 3 | 1 | 0 | 0 | 0 | 0 | 1 | 270 |

## Por sección (top 50)

| Sección | Páginas | 2xx | 3xx | 4xx | 5xx | Errores | Bloq. | noindex | Media (ms) |
|---|---|---|---|---|---|---|---|---|---|
| docs | 7 | 3 | 4 | 0 | 0 | 0 | 0 | 1 | 91 |
| e | 5 | 0 | 0 | 0 | 0 | 5 | 0 | 0 | 0 |
| a | 3 | 0 | 3 | 0 | 0 | 0 | 0 | 0 | 10 |
| blog | 2 | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 455 |
| wöhner | 2 | 2 | 0 | 0 | 0 | 0 | 0 | 0 | 125 |
| (raíz) | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 0 | 100 |
| ext | 1 | 0 | 1 | 0 | 0 | 0 | 0 | 0 | 10 |
| legal | 1 | 0 | 1 | 0 | 0 | 0 | 0 | 0 | 10 |
| old | 1 | 0 | 1 | 0 | 0 | 0 | 0 | 0 | 10 |
| privado | 1 | 0 | 0 | 0 | 0 | 0 | 1 | 0 | 0 |
| roto-404 | 1 | 0 | 0 | 1 | 0 | 0 | 0 | 0 | 20 |
| roto-410 | 1 | 0 | 0 | 1 | 0 | 0 | 0 | 0 | 20 |
| roto-500 | 1 | 0 | 0 | 0 | 1 | 0 | 0 | 0 | 20 |

## Tipos de contenido

| Tipo | Páginas |
|---|---|
| text/html | 7 |

## Redirecciones

| Patrón | Páginas | Ejemplo de origen | Ejemplo de destino |
|---|---|---|---|
| segmento 2: a → b | 3 | https://example.com/en/a/1 | https://example.com/en/b/1 |
| añade barra final | 1 | https://example.com/en/docs | https://example.com/en/docs/ |
| añade prefijo /en | 1 | https://example.com/legal | https://example.com/en/legal |
| cambia mayúsculas | 1 | https://example.com/en/docs/J | https://example.com/en/docs/j |
| http→https | 1 | http://example.com/en/docs/h | https://example.com/en/docs/h |
| otra ruta | 1 | https://example.com/old | https://example.com/nuevo/sitio |
| otro host | 1 | https://example.com/ext | https://otro.example.org/ |
| quita barra final | 1 | https://example.com/es/blog/x/ | https://example.com/es/blog/x |
| quita query | 1 | https://example.com/en/docs/k?utm=1 | https://example.com/en/docs/k |

## Errores

| Tipo | Páginas | URL de ejemplo |
|---|---|---|
| conexión rechazada | 1 | https://example.com/e/refused |
| dns | 1 | https://example.com/e/dns |
| otro: Get "": algo raro ha pasado | 1 | https://example.com/e/otro |
| timeout | 1 | https://example.com/e/timeout |
| tls | 1 | https://example.com/e/tls |

## Páginas rotas con más referrers

Sobre 8 páginas rotas analizadas.

| Referrers | Código | URL | Error |
|---|---|---|---|
| 3 | 404 | https://example.com/roto-404 |  |
| 2 | 500 | https://example.com/roto-500 |  |
| 1 | 410 | https://example.com/roto-410 |  |
| 0 | ERR | https://example.com/e/dns | dial tcp: lookup nope.example.com: no such host |
| 0 | ERR | https://example.com/e/otro | Get "https://example.com/e/otro": algo raro ha pasado |
| 0 | ERR | https://example.com/e/refused | dial tcp 127.0.0.1:1: connect: connection refused |
| 0 | ERR | https://example.com/e/timeout | Get "https://example.com/e/timeout": context deadline exceeded |
| 0 | ERR | https://example.com/e/tls | x509: certificate signed by unknown authority |

## Títulos duplicados

| Páginas | Título | URL de ejemplo |
|---|---|---|
| 2 | Otra | https://example.com/es/blog/d |
| 2 | guía | https://example.com/en/docs/b |

## Páginas más lentas

| Tiempo (ms) | Código | Prof. | URL |
|---|---|---|---|
| 900 | 200 | 2 | https://example.com/es/blog/d |
| 300 | 200 | 1 | https://example.com/en/docs/a |
| 250 | 200 | 1 | https://example.com/en/docs/b |
| 130 | 200 | 3 | https://example.com/w%C3%B6hner/g |
| 120 | 200 | 3 | https://example.com/es/w%C3%B6hner/e |
| 100 | 200 | 0 | https://example.com/ |
| 50 | 200 | 2 | https://example.com/es/docs/c |
| 20 | 404 | 2 | https://example.com/roto-404 |
| 20 | 410 | 2 | https://example.com/roto-410 |
| 20 | 500 | 2 | https://example.com/roto-500 |

## Páginas más grandes

| Tamaño (bytes) | Código | Prof. | URL |
|---|---|---|---|
| 9.000 | 200 | 2 | https://example.com/es/blog/d |
| 2.500 | 200 | 1 | https://example.com/en/docs/b |
| 2.000 | 200 | 1 | https://example.com/en/docs/a |
| 1.000 | 200 | 0 | https://example.com/ |
| 800 | 200 | 3 | https://example.com/es/w%C3%B6hner/e |
| 700 | 200 | 3 | https://example.com/w%C3%B6hner/g |
| 500 | 200 | 2 | https://example.com/es/docs/c |
| 0 | 301 | 1 | http://example.com/en/docs/h |
| 0 | 301 | 1 | https://example.com/en/a/1 |
| 0 | 301 | 1 | https://example.com/en/a/2 |

## Muestras

### Redirecciones (3xx)

| Código | Prof. | URL | Detalle |
|---|---|---|---|
| 301 | 1 | https://example.com/en/docs | https://example.com/en/docs/ |
| 301 | 2 | https://example.com/es/blog/x/ | https://example.com/es/blog/x |
| 301 | 1 | http://example.com/en/docs/h | https://example.com/en/docs/h |
| 301 | 1 | https://example.com/en/docs/J | https://example.com/en/docs/j |
| 302 | 1 | https://example.com/en/docs/k?utm=1 | https://example.com/en/docs/k |
| 302 | 1 | https://example.com/ext | https://otro.example.org/ |
| 301 | 1 | https://example.com/en/a/2 | https://example.com/en/b/2 |
| 301 | 1 | https://example.com/en/a/3 | https://example.com/en/b/3 |
| 301 | 1 | https://example.com/legal | https://example.com/en/legal |
| 301 | 1 | https://example.com/old | https://example.com/nuevo/sitio |

### No encontradas (4xx)

| Código | Prof. | URL | Detalle |
|---|---|---|---|
| 404 | 2 | https://example.com/roto-404 |  |
| 410 | 2 | https://example.com/roto-410 |  |

### Errores de servidor (5xx)

| Código | Prof. | URL | Detalle |
|---|---|---|---|
| 500 | 2 | https://example.com/roto-500 |  |

### Fallos de red

| Código | Prof. | URL | Detalle |
|---|---|---|---|
| ERR | 2 | https://example.com/e/timeout | Get "https://example.com/e/timeout": context deadline exceeded |
| ERR | 2 | https://example.com/e/dns | dial tcp: lookup nope.example.com: no such host |
| ERR | 2 | https://example.com/e/refused | dial tcp 127.0.0.1:1: connect: connection refused |
| ERR | 2 | https://example.com/e/tls | x509: certificate signed by unknown authority |
| ERR | 2 | https://example.com/e/otro | Get "https://example.com/e/otro": algo raro ha pasado |

### Bloqueadas por robots

| Código | Prof. | URL | Detalle |
|---|---|---|---|
| BLOQ | 1 | https://example.com/privado |  |

### noindex

| Código | Prof. | URL | Detalle |
|---|---|---|---|
| 200 | 2 | https://example.com/es/docs/c |  |
