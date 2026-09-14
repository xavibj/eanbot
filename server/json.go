package server

import (
	"encoding/json"
	"net/http"
)

// writeJSON writes v as the JSON response body with the given status code.
// json.Encoder.Encode appends a trailing newline, satisfying the "salto de
// línea final" requirement from specs/004-api-web.md.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErrors writes the standard {"errors": [...]} error envelope.
func writeErrors(w http.ResponseWriter, status int, errs []string) {
	writeJSON(w, status, map[string]any{"errors": errs})
}
