package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"xavi.net/eanbot/report"
	"xavi.net/eanbot/store"
)

// cmdCrawls implements "eanbot crawls [flags]".
func cmdCrawls(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("crawls", stderr)
	dbPath := fs.String("db", "eanbot.db", "ruta de la base de datos SQLite")
	jsonOutput := fs.Bool("json", false, "imprimir el resultado en JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "error: argumentos no reconocidos: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer st.Close()

	crawls, err := st.ListCrawls()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if *jsonOutput {
		return writeJSONLine(stdout, stderr, struct {
			Crawls []store.Crawl `json:"crawls"`
		}{crawls})
	}

	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tESTADO\tPÁGINAS\tINICIO\tSEMILLA")
	for _, c := range crawls {
		fmt.Fprintf(tw, "%d\t%s\t%d\t%s\t%s\n",
			c.ID, c.Status, c.PagesCount, c.StartedAt.Format("2006-01-02 15:04:05"), c.Seed)
	}
	return flushTabwriter(tw, stderr)
}

// cmdPages implements "eanbot pages <crawl-id> [flags]".
func cmdPages(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("pages", stderr)
	dbPath := fs.String("db", "eanbot.db", "ruta de la base de datos SQLite")
	status := fs.String("status", "", "filtrar por estado: 2xx|3xx|4xx|5xx|error|blocked|via_nofollow")
	query := fs.String("q", "", "filtrar por texto en la URL o el título")
	jsonOutput := fs.Bool("json", false, "imprimir el resultado en JSON")

	crawlID, args, code := shiftCrawlID(args, stderr)
	if code != 0 {
		return code
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "error: argumentos no reconocidos: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	if crawlID < 0 {
		fmt.Fprintln(stderr, "error: rastreo no encontrado")
		return 1
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer st.Close()

	if _, err := st.GetCrawl(crawlID); err != nil {
		return reportCrawlLookupError(stderr, err)
	}

	pages, total, err := st.ListPages(crawlID, store.PageFilter{Status: *status, Query: *query})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if *jsonOutput {
		return writeJSONLine(stdout, stderr, struct {
			Pages []store.Page `json:"pages"`
			Total int          `json:"total"`
		}{pages, total})
	}

	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "CÓDIGO\tTIPO\tPROF.\tVÍA\tURL\tTÍTULO")
	for _, p := range pages {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\n", storePageCode(p), p.ContentType, p.Depth, viaColumn(p), p.URL, p.Title)
	}
	return flushTabwriter(tw, stderr)
}

// viaColumn is the "VÍA" column of "eanbot pages": "nofollow" for a page
// discovered only through nofollow links, "" otherwise.
func viaColumn(p store.Page) string {
	if p.ViaNoFollow {
		return "nofollow"
	}
	return ""
}

// brokenBatchSize is how many broken pages cmdBroken fetches from the store
// per call to BrokenLinks/Referrers, so a crawl with tens of thousands of
// broken pages is streamed rather than loaded (and paginated) all at once.
const brokenBatchSize = 1000

// brokenPageJSON is one entry of "eanbot broken -json"'s "broken" array:
// the broken page, its total referrer count, and up to -referrers of the
// actual referring links.
type brokenPageJSON struct {
	Page           store.Page   `json:"page"`
	ReferrersCount int          `json:"referrers_count"`
	Referrers      []store.Link `json:"referrers"`
}

// cmdBroken implements "eanbot broken <crawl-id> [flags]".
func cmdBroken(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("broken", stderr)
	dbPath := fs.String("db", "eanbot.db", "ruta de la base de datos SQLite")
	referrersN := fs.Int("referrers", 3, "nº de referrers a mostrar por página rota")
	jsonOutput := fs.Bool("json", false, "imprimir el resultado en JSON")

	crawlID, args, code := shiftCrawlID(args, stderr)
	if code != 0 {
		return code
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "error: argumentos no reconocidos: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	if crawlID < 0 {
		fmt.Fprintln(stderr, "error: rastreo no encontrado")
		return 1
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer st.Close()

	result, total, err := collectBrokenPages(st, crawlID, *referrersN)
	if err != nil {
		return reportCrawlLookupError(stderr, err)
	}

	if *jsonOutput {
		return writeJSONLine(stdout, stderr, struct {
			Broken []brokenPageJSON `json:"broken"`
			Total  int              `json:"total"`
		}{result, total})
	}

	for _, b := range result {
		fmt.Fprintf(stdout, "%s  %d  %s\n", storePageCode(b.Page), b.ReferrersCount, b.Page.URL)
		for _, r := range b.Referrers {
			if r.Text != "" {
				fmt.Fprintf(stdout, "    <- %s (%q)\n", r.FromURL, r.Text)
			} else {
				fmt.Fprintf(stdout, "    <- %s\n", r.FromURL)
			}
		}
	}
	return 0
}

// collectBrokenPages walks store.BrokenLinks in batches of brokenBatchSize
// and fills in up to referrersPerPage referrers per broken page (one
// store.Referrers call per batch), so a crawl with a large number of broken
// pages is never loaded into a single unbounded query.
func collectBrokenPages(st *store.Store, crawlID int64, referrersPerPage int) ([]brokenPageJSON, int, error) {
	var (
		result []brokenPageJSON
		total  int
		offset int
	)
	for {
		batch, t, err := st.BrokenLinks(crawlID, brokenBatchSize, offset)
		if err != nil {
			return nil, 0, err
		}
		total = t
		if len(batch) == 0 {
			break
		}

		urls := make([]string, len(batch))
		for i, bp := range batch {
			urls[i] = bp.Page.URL
		}
		refsByURL, err := st.Referrers(crawlID, urls, referrersPerPage)
		if err != nil {
			return nil, 0, err
		}
		for _, bp := range batch {
			result = append(result, brokenPageJSON{
				Page:           bp.Page,
				ReferrersCount: bp.ReferrersCount,
				Referrers:      refsByURL[bp.Page.URL],
			})
		}

		offset += len(batch)
		if offset >= total {
			break
		}
	}
	return result, total, nil
}

// shiftCrawlID extracts the leading positional <crawl-id> argument shared by
// "pages", "broken" and "report", returning the remaining args to feed the FlagSet.
// A non-numeric id is reported as id == -1 (translated by the caller into
// "rastreo no encontrado", exit 1) rather than a flag-parsing error, per
// specs/005-cli.md.
func shiftCrawlID(args []string, stderr io.Writer) (id int64, rest []string, code int) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, "error: se requiere el id del rastreo")
		return 0, nil, 2
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return -1, args[1:], 0
	}
	return id, args[1:], 0
}

// reportCrawlLookupError turns a store error from GetCrawl/BrokenLinks/
// report.Build into
// the CLI's exit code and message: ErrNotFound is reported as "rastreo no
// encontrado", anything else as a generic error.
func reportCrawlLookupError(stderr io.Writer, err error) int {
	if errors.Is(err, store.ErrNotFound) {
		fmt.Fprintln(stderr, "error: rastreo no encontrado")
	} else {
		fmt.Fprintf(stderr, "error: %v\n", err)
	}
	return 1
}

// storePageCode is the short status column used by "pages" and "broken":
// the numeric HTTP status, or BLOQ/ERR when no request was made or it
// failed.
func storePageCode(p store.Page) string {
	switch {
	case p.Blocked:
		return "BLOQ"
	case p.Status == 0:
		return "ERR"
	default:
		return strconv.Itoa(p.Status)
	}
}

// writeJSONLine marshals v to stdout followed by a newline.
func writeJSONLine(stdout, stderr io.Writer, v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	stdout.Write(b)
	fmt.Fprintln(stdout)
	return 0
}

// flushTabwriter flushes tw, reporting any error as a normal execution
// failure.
func flushTabwriter(tw *tabwriter.Writer, stderr io.Writer) int {
	if err := tw.Flush(); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// cmdReport implements "eanbot report <crawl-id> [flags]" (spec 008): the
// aggregate report of a crawl, as Markdown (default) or JSON, to stdout or
// to a file.
func cmdReport(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("report", stderr)
	dbPath := fs.String("db", "eanbot.db", "ruta de la base de datos SQLite")
	jsonOutput := fs.Bool("json", false, "imprimir el informe en JSON en lugar de Markdown")
	outPath := fs.String("o", "", "escribir el informe en un fichero en lugar de stdout")

	crawlID, args, code := shiftCrawlID(args, stderr)
	if code != 0 {
		return code
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "error: argumentos no reconocidos: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	if crawlID < 0 {
		fmt.Fprintln(stderr, "error: rastreo no encontrado")
		return 1
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer st.Close()

	rep, err := report.Build(ctx, st, crawlID)
	if err != nil {
		return reportCrawlLookupError(stderr, err)
	}

	var body []byte
	if *jsonOutput {
		body, err = json.Marshal(rep)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		body = append(body, '\n')
	} else {
		body = []byte(report.Markdown(rep))
	}

	if *outPath == "" {
		if _, err := stdout.Write(body); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}

	if err := os.WriteFile(*outPath, body, 0o644); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "informe escrito en %s\n", *outPath)
	return 0
}
