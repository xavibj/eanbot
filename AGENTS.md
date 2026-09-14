# AGENTS.md — reglas para agentes que trabajan en eanbot

1. **SDD**: `specs/` es el contrato. Lee `specs/001-plan-tecnico.md` y la spec
   de tu fase antes de escribir código. Si tu cambio contradice una spec,
   actualiza la spec en el mismo cambio y dilo en el informe.
2. **TDD**: tests primero, confirma que fallan, implementación mínima,
   refactor. Table-driven, `httptest`, BBDD real en `t.TempDir()`.
3. **Terminado** = `go test ./...`, `go vet ./...` y `gofmt -l .` limpios en
   TODO el repo.
4. **Propiedad de ficheros**: toca solo las rutas asignadas en
   `specs/006-fases.md`. Nunca `go.mod`/`go.sum`, nunca `go mod tidy`.
5. **Dependencias**: solo las de `specs/001-plan-tecnico.md`.
6. Código y comentarios en inglés; textos de UI en español.
7. Temporales fuera del repo (scratchpad). No commitees; el orquestador
   commitea.
8. Informe final compacto: ficheros tocados, decisiones y su porqué,
   desviaciones de spec, salida resumida de la suite.
