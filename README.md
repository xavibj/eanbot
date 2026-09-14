# eanbot

Rastreador web («bot tipo Googlebot») en un único binario Go: CLI, API REST y
interfaz Vue embebida. Rastrea un sitio desde una URL semilla respetando
`robots.txt` y guarda páginas, códigos, metadatos y enlaces en SQLite.

```sh
make build
./bin/eanbot crawl https://xavibolivar.xavi.net -max-pages 200
./bin/eanbot serve            # http://localhost:8345
```

Especificaciones en `specs/`; reglas para agentes en `AGENTS.md`.
