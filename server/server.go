// Package server implements eanbot's REST API, the background crawl
// Manager and the embedded static frontend. It depends on store and
// crawler but is never imported by them.
package server

import (
	"context"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"xavi.net/eanbot/crawler"
	"xavi.net/eanbot/store"
)

// Options configures a Server.
type Options struct {
	// NewFetcher builds the crawler.Fetcher used for a given crawl. If nil,
	// crawler.NewHTTPFetcher is used, so tests can inject a fake Fetcher
	// and never touch the network.
	NewFetcher func(cfg crawler.Config) crawler.Fetcher

	// Logger receives a line for every crawl start/finish and every
	// internal error. If nil, log output is discarded.
	Logger *log.Logger
}

// Server holds everything needed to serve eanbot's API and frontend: the
// store, the background crawl Manager and the embedded static assets.
type Server struct {
	st     *store.Store
	mgr    *manager
	log    *log.Logger
	static fs.FS
}

// New builds a Server backed by st. It does not start listening; call
// Handler to obtain an http.Handler and serve it yourself.
func New(st *store.Store, opts Options) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	newFetcher := opts.NewFetcher
	if newFetcher == nil {
		newFetcher = func(cfg crawler.Config) crawler.Fetcher {
			hf := crawler.NewHTTPFetcher(cfg.UserAgent, cfg.Timeout, cfg.MaxBodyBytes)
			hf.Headers = cfg.Headers
			hf.Origin = cfg.Origin
			hf.InsecureTLS = cfg.InsecureTLS
			if normalized, err := crawler.Normalize(cfg.Seed, nil); err == nil {
				if u, err := url.Parse(normalized); err == nil {
					hf.OriginHost = u.Hostname()
				}
			}
			return hf
		}
	}
	return &Server{
		st:     st,
		log:    logger,
		static: staticFS(),
		mgr:    newManager(st, newFetcher, logger),
	}
}

// Handler returns the http.Handler serving both the JSON API (under /api/)
// and the embedded static frontend (everything else).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/healthz", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet: s.handleHealthz,
	}))
	mux.HandleFunc("/api/crawls", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet:  s.handleListCrawls,
		http.MethodPost: s.handlePostCrawl,
	}))
	mux.HandleFunc("/api/crawls/{id}", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet:    s.wrapID(s.handleGetCrawl),
		http.MethodDelete: s.wrapID(s.handleDeleteCrawl),
	}))
	mux.HandleFunc("/api/crawls/{id}/cancel", methodHandler(map[string]http.HandlerFunc{
		http.MethodPost: s.wrapID(s.handleCancelCrawl),
	}))
	mux.HandleFunc("/api/crawls/{id}/pages", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet: s.wrapID(s.handleListPages),
	}))
	mux.HandleFunc("/api/crawls/{id}/pages/{page_id}", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet: s.wrapPageID(s.handleGetPage),
	}))
	mux.HandleFunc("/api/crawls/{id}/broken", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet: s.wrapID(s.handleBroken),
	}))
	mux.HandleFunc("/api/crawls/{id}/report", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet: s.wrapID(s.handleReport),
	}))
	// Any other /api/ path: JSON 404 (more specific patterns above win).
	mux.HandleFunc("/api/", s.handleAPIFallback)

	// Everything else: embedded static frontend with SPA fallback.
	mux.HandleFunc("/", s.handleStatic)

	return mux
}

// Shutdown cancels every crawl currently running and waits for their
// goroutines to finish, or for ctx to be done, whichever happens first.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.mgr.shutdown(ctx)
}

// --- routing helpers ---

// methodHandler dispatches to the handler registered for the request's
// method, or replies 405 with an Allow header listing the methods that are
// registered for this pattern.
func methodHandler(handlers map[string]http.HandlerFunc) http.HandlerFunc {
	allowed := make([]string, 0, len(handlers))
	for m := range handlers {
		allowed = append(allowed, m)
	}
	sort.Strings(allowed)
	allow := strings.Join(allowed, ", ")

	return func(w http.ResponseWriter, r *http.Request) {
		if h, ok := handlers[r.Method]; ok {
			h(w, r)
			return
		}
		w.Header().Set("Allow", allow)
		writeErrors(w, http.StatusMethodNotAllowed, []string{"método no permitido"})
	}
}

// wrapID parses the {id} path value as an int64 before calling h; a
// non-numeric id is reported as 404, per spec ("{id} no numérico → 404").
func (s *Server) wrapID(h func(w http.ResponseWriter, r *http.Request, id int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			s.notFound(w)
			return
		}
		h(w, r, id)
	}
}

// wrapPageID parses both {id} and {page_id} as int64s before calling h.
func (s *Server) wrapPageID(h func(w http.ResponseWriter, r *http.Request, crawlID, pageID int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err1 := strconv.ParseInt(r.PathValue("id"), 10, 64)
		pageID, err2 := strconv.ParseInt(r.PathValue("page_id"), 10, 64)
		if err1 != nil || err2 != nil {
			s.notFound(w)
			return
		}
		h(w, r, id, pageID)
	}
}

func (s *Server) handleAPIFallback(w http.ResponseWriter, r *http.Request) {
	s.notFound(w)
}

// --- JSON response helpers ---

func (s *Server) notFound(w http.ResponseWriter) {
	writeErrors(w, http.StatusNotFound, []string{"no encontrado"})
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.log.Printf("internal error: %v", err)
	writeErrors(w, http.StatusInternalServerError, []string{"error interno"})
}
