# 000 — Especificación de producto: eanbot

## Qué es

`eanbot` es un rastreador web («bot tipo Googlebot») que, partiendo de una URL
semilla, descubre y visita todas las páginas de ese sitio siguiendo enlaces,
respetando `robots.txt` y guardando el resultado de cada página (código HTTP,
tipo de contenido, título, meta description, canonical, enlaces salientes y
entrantes, redirecciones y errores). Sirve para auditar un sitio propio: ver qué
URLs existen, cuáles fallan, qué enlaces están rotos y desde dónde se enlazan.

Primer objetivo: rastrear la web del autor (`https://xavibolivar.xavi.net`, a la
que redirige `https://xavi.net`). Después, cualquier sitio.

## Forma

Un único binario Go (ver `specs/001-plan-tecnico.md`) con:

- **CLI**: `eanbot crawl <url>` rastrea y guarda en SQLite; `eanbot serve`
  levanta la interfaz web; `eanbot crawls` y `eanbot pages` consultan.
- **API REST** JSON bajo `/api/`.
- **Interfaz web** Vue 3 embebida: lanzar rastreos, ver progreso, tabla de
  páginas con filtros, enlaces rotos con referrers, detalle de página.

## Comportamiento del bot

1. **Semilla y ámbito**. Se rastrea solo el host de la semilla (tras normalizar).
   `www.host` y `host` se consideran el mismo host. Opción `include_subdomains`
   para incluir `*.host`. Las URLs fuera de ámbito se registran como enlaces
   salientes pero no se visitan.
2. **robots.txt**. Antes de la primera petición se descarga
   `<scheme>://<host>/robots.txt`. Se aplica el grupo cuyo `User-agent` coincide
   con el token del bot (`eanbot`, sin distinguir mayúsculas), o el grupo `*`.
   Se respetan `Disallow`/`Allow` (coincidencia más larga gana; `Allow` gana en
   empate; comodín `*` y ancla `$`), `Crawl-delay` (si es mayor que el retardo
   configurado, manda) y `Sitemap:` (se leen para descubrir URLs). Respuesta
   4xx → todo permitido. 5xx o error de red → todo prohibido (como Googlebot).
   Opción `ignore_robots` para sitios propios.
3. **Recorrido**. Anchura (BFS) desde la semilla (profundidad 0). Límites:
   `max_pages` (URLs solicitadas, cualquier código), `max_depth`. `concurrency`
   peticiones simultáneas y un retardo mínimo `delay` entre inicios de petición
   al host (cortesía).
4. **Redirecciones**. No se siguen automáticamente: un 3xx se registra como
   página con `redirect_to`, y el destino se encola (si está en ámbito) con la
   misma profundidad.
5. **Contenido**. Solo se parsean enlaces en `text/html`. Otros tipos (imágenes,
   PDF, CSS...) se solicitan y se registra código, tipo y tamaño, sin descargar
   más de `max_body_bytes`.
6. **Enlaces**. `<a href>` (absolutizados contra `<base href>` o la URL de la
   página), ignorando `mailto:`, `tel:`, `javascript:`, `data:` y fragmentos.
   Se registra `rel=nofollow`; los nofollow y las páginas con `meta robots
   nofollow` no aportan URLs nuevas a la cola, pero el enlace queda guardado.
7. **Identidad**. `User-Agent: EANBot/0.1 (+https://xavi.net)` por defecto.
8. **Persistencia**. Cada rastreo es un registro con su configuración, estado
   (`running`, `done`, `failed`, `cancelled`), contadores y páginas.

## Historias de usuario

- Como administrador, lanzo `eanbot crawl https://xavibolivar.xavi.net` y al
  terminar veo un resumen: páginas por clase de código, errores, bloqueadas.
- Como administrador, abro la web, creo un rastreo, veo el contador subir y,
  al acabar, filtro las páginas 404 y veo desde qué páginas se enlazan.
- Como administrador, abro una página concreta y veo título, description,
  canonical, meta robots, enlaces entrantes y salientes.
- Como administrador, cancelo un rastreo en marcha y borro rastreos viejos.

## Fuera de alcance (v1)

Renderizado de JavaScript, autenticación en el sitio rastreado, rastreo de
varios hosts a la vez, programación periódica, exportación CSV.
