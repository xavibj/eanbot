package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"xavi.net/eanbot/report"
	"xavi.net/eanbot/store"
)

// maxRequestBody is the largest request body accepted by any /api/
// endpoint, per specs/004-api-web.md ("Content-Length excesivo o body > 1
// MiB → 400").
const maxRequestBody = 1 << 20 // 1 MiB

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePostCrawl(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > maxRequestBody {
		writeErrors(w, http.StatusBadRequest, []string{"json malformado"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	var doc configDoc
	if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
		writeErrors(w, http.StatusBadRequest, []string{"json malformado"})
		return
	}

	cfg := configToCrawlerConfig(doc)
	id, err := s.mgr.Start(cfg)
	if err != nil {
		var verr *ValidationError
		if errors.As(err, &verr) {
			writeErrors(w, http.StatusBadRequest, verr.Errors)
			return
		}
		s.internalError(w, err)
		return
	}

	crawl, err := s.st.GetCrawl(id)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, crawl)
}

func (s *Server) handleListCrawls(w http.ResponseWriter, r *http.Request) {
	crawls, err := s.st.ListCrawls()
	if err != nil {
		s.internalError(w, err)
		return
	}
	if crawls == nil {
		crawls = []store.Crawl{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"crawls": crawls})
}

func (s *Server) handleGetCrawl(w http.ResponseWriter, r *http.Request, id int64) {
	// isRunning is read before the store, not after: the Manager only
	// removes a crawl from its running set once the crawl's finished
	// status has already been committed (see manager.run), so reading
	// isRunning first guarantees that running == false implies the store
	// already reflects the final status. Reading it last could otherwise
	// observe a stale "running" store row alongside running == false, if
	// the crawl finished in between the two reads.
	running := s.mgr.isRunning(id)

	crawl, err := s.st.GetCrawl(id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}

	summary, err := s.st.Summarize(id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"crawl":   crawl,
		"summary": summary,
		"running": running,
	})
}

func (s *Server) handleCancelCrawl(w http.ResponseWriter, r *http.Request, id int64) {
	crawl, err := s.st.GetCrawl(id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}

	// Cancel() returns false when the crawl was not running; per spec that
	// is still a 200 with the (unchanged) crawl object, not an error.
	s.mgr.Cancel(id)

	writeJSON(w, http.StatusOK, map[string]any{"crawl": crawl})
}

// deleteCancelTimeout bounds how long DELETE waits for a running crawl to
// actually stop before deleting its rows.
const deleteCancelTimeout = 10 * time.Second

func (s *Server) handleDeleteCrawl(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := s.st.GetCrawl(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w)
			return
		}
		s.internalError(w, err)
		return
	}

	if s.mgr.isRunning(id) {
		s.mgr.Cancel(id)
		deadline := time.Now().Add(deleteCancelTimeout)
		for s.mgr.isRunning(id) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}

	if err := s.st.DeleteCrawl(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w)
			return
		}
		s.internalError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListPages(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := s.st.GetCrawl(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w)
			return
		}
		s.internalError(w, err)
		return
	}

	limit, offset := parseLimitOffset(r)
	q := r.URL.Query()

	filter := store.PageFilter{
		Status: q.Get("status"),
		Query:  q.Get("q"),
		Limit:  limit,
		Offset: offset,
	}

	pages, total, err := s.st.ListPages(id, filter)
	if err != nil {
		if err.Error() == "status no válido" {
			writeErrors(w, http.StatusBadRequest, []string{"status no válido"})
			return
		}
		s.internalError(w, err)
		return
	}
	if pages == nil {
		pages = []store.Page{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"pages":  pages,
		"total":  total,
		"limit":  filter.Limit,
		"offset": filter.Offset,
	})
}

func (s *Server) handleGetPage(w http.ResponseWriter, r *http.Request, crawlID, pageID int64) {
	detail, err := s.st.GetPage(crawlID, pageID)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := detail.Outlinks
	if out == nil {
		out = []store.Link{}
	}
	in := detail.Inlinks
	if in == nil {
		in = []store.Link{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"page":           detail.Page,
		"outlinks":       out,
		"inlinks":        in,
		"outlinks_total": detail.OutlinksTotal,
		"inlinks_total":  detail.InlinksTotal,
	})
}

// parseLimitOffset extracts and clamps the limit/offset query params shared
// by the paginated /pages and /broken endpoints (limit defaults to 100,
// capped at 1000; offset defaults to 0).
func parseLimitOffset(r *http.Request) (limit, offset int) {
	q := r.URL.Query()
	limit, _ = strconv.Atoi(q.Get("limit"))
	if limit < 1 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	offset, _ = strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (s *Server) handleBroken(w http.ResponseWriter, r *http.Request, id int64) {
	limit, offset := parseLimitOffset(r)

	broken, total, err := s.st.BrokenLinks(id, limit, offset)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	if broken == nil {
		broken = []store.BrokenPage{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"broken": broken,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// handleReport serves the aggregate report of a crawl (specs/008-informe.md)
// as JSON or Markdown. The report is always computed on demand -- it is
// never cached, and never produced by polling -- so it uses r.Context():
// if the client goes away mid-scan, the pass over the crawl's pages stops
// with it instead of finishing a report nobody will read.
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request, id int64) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "md" {
		writeErrors(w, http.StatusBadRequest, []string{"format no válido"})
		return
	}

	rep, err := report.Build(r.Context(), s.st, id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w)
		return
	}
	if err != nil {
		if r.Context().Err() != nil {
			// The client hung up mid-report: nothing left to answer to.
			return
		}
		s.internalError(w, err)
		return
	}

	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="eanbot-rastreo-%d.%s"`, id, format))
	}

	if format == "md" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, report.Markdown(rep))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": rep})
}
