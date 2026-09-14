package server

import (
	"embed"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

// staticFiles embeds the compiled Vite frontend (server/static/), or the
// minimal placeholder until the real build lands (see specs/004-api-web.md).
//
//go:embed static
var staticFiles embed.FS

// staticFS strips the "static/" prefix embed.FS keeps, so paths served
// match the request path exactly ("index.html", "assets/app.js", ...).
func staticFS() fs.FS {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		// staticFiles always contains a "static" directory: the embed
		// directive above would fail to compile otherwise.
		panic(err)
	}
	return sub
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	serveStatic(s.static, w, r)
}

// serveStatic serves fsys as a single-page app: an existing file is served
// with its own headers, and anything else (including /api/-free paths
// with no matching file) falls back to index.html with a 200, so that
// client-side routing survives a reload. It is a free function, rather
// than a Server method, so tests can exercise it against a synthetic
// fs.FS (e.g. testing/fstest.MapFS) without needing real Vite output.
func serveStatic(fsys fs.FS, w http.ResponseWriter, r *http.Request) {
	upath := strings.TrimPrefix(r.URL.Path, "/")
	if upath == "" {
		upath = "index.html"
	}

	f, info, ok := openStaticFile(fsys, upath)
	if !ok {
		upath = "index.html"
		f, info, ok = openStaticFile(fsys, upath)
		if !ok {
			writeErrors(w, http.StatusInternalServerError, []string{"error interno"})
			return
		}
	}
	defer f.Close()

	setStaticHeaders(w, upath)
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, upath, info.ModTime(), rs)
		return
	}
	_, _ = io.Copy(w, f)
}

// openStaticFile opens upath in fsys, reporting ok=false for anything that
// is missing or is a directory (never served directly).
func openStaticFile(fsys fs.FS, upath string) (f fs.File, info fs.FileInfo, ok bool) {
	f, err := fsys.Open(upath)
	if err != nil {
		return nil, nil, false
	}
	info, err = f.Stat()
	if err != nil || info.IsDir() {
		f.Close()
		return nil, nil, false
	}
	return f, info, true
}

// setStaticHeaders sets Content-Type and Cache-Control for a static file
// served at upath, per specs/004-api-web.md: hashed files under assets/
// are cached forever, everything else (index.html, and the SPA fallback,
// which also serves index.html) is revalidated on every load.
func setStaticHeaders(w http.ResponseWriter, upath string) {
	setContentType(w, upath)
	if strings.HasPrefix(upath, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
}

// setContentType sets the Content-Type header for upath. mime.TypeByExtension
// is system-dependent for ".js"/".css" (some systems map .js to
// "application/javascript" or omit it), so those two are forced explicitly.
func setContentType(w http.ResponseWriter, upath string) {
	ext := filepath.Ext(upath)
	switch ext {
	case ".js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		return
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		return
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
}
