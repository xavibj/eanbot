package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"xavi.net/eanbot/crawler"
	"xavi.net/eanbot/store"
)

// cmdCrawl implements "eanbot crawl <url> [flags]": it opens the store,
// builds and validates a crawler.Config from flags, runs the crawl with a
// real HTTPFetcher and a Sink that persists every page, then prints a
// summary. See specs/005-cli.md for the full contract.
func cmdCrawl(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("crawl", stderr)
	defaults := crawler.Defaults()

	dbPath := fs.String("db", "eanbot.db", "ruta de la base de datos SQLite")
	maxPages := fs.Int("max-pages", defaults.MaxPages, "número máximo de páginas a visitar")
	maxDepth := fs.Int("max-depth", defaults.MaxDepth, "profundidad máxima desde la semilla")
	concurrency := fs.Int("concurrency", defaults.Concurrency, "número de workers concurrentes")
	delay := fs.Duration("delay", defaults.Delay, "cortesía entre peticiones (0 para desactivarla)")
	timeout := fs.Duration("timeout", defaults.Timeout, "tiempo máximo por petición")
	userAgent := fs.String("user-agent", defaults.UserAgent, "cabecera User-Agent")
	includeSubdomains := fs.Bool("include-subdomains", false, "incluir subdominios en el ámbito del rastreo")
	ignoreRobots := fs.Bool("ignore-robots", false, "ignorar robots.txt")
	noSitemaps := fs.Bool("no-sitemaps", false, "no usar sitemaps.xml para descubrir URLs")
	jsonOutput := fs.Bool("json", false, "imprimir el resultado en JSON")
	quiet := fs.Bool("quiet", false, "no mostrar el progreso en stderr")
	origin := fs.String("origin", "", "IP (o ip:puerto) del servidor de origen, saltando Cloudflare (Host y SNI intactos)")
	insecureTLS := fs.Bool("insecure-tls", false, "no verificar el certificado TLS")

	headers := map[string]string{}
	fs.Func("header", `cabecera adicional "Nombre: valor" para todas las peticiones (repetible)`, func(v string) error {
		name, value, ok := strings.Cut(v, ":")
		if !ok {
			return fmt.Errorf("cabecera sin ':': %q", v)
		}
		headers[strings.TrimSpace(name)] = strings.TrimSpace(value)
		return nil
	})

	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, "error: se requiere una URL semilla")
		return 2
	}
	seed := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "error: argumentos no reconocidos: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}

	cfg := crawler.Config{
		Seed:              seed,
		MaxPages:          *maxPages,
		MaxDepth:          *maxDepth,
		Concurrency:       *concurrency,
		Delay:             *delay,
		Timeout:           *timeout,
		MaxBodyBytes:      defaults.MaxBodyBytes,
		UserAgent:         *userAgent,
		RobotsToken:       defaults.RobotsToken,
		IncludeSubdomains: *includeSubdomains,
		IgnoreRobots:      *ignoreRobots,
		UseSitemaps:       !*noSitemaps,
		Headers:           headers,
		Origin:            *origin,
		InsecureTLS:       *insecureTLS,
	}

	if errs := cfg.Validate(); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(stderr, "error: %s\n", e)
		}
		return 2
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer st.Close()

	configJSON, err := crawlConfigJSON(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	crawl, err := st.CreateCrawl(cfg.Seed, configJSON)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	fetcher := crawler.NewHTTPFetcher(cfg.UserAgent, cfg.Timeout, cfg.MaxBodyBytes)
	fetcher.Headers = cfg.Headers
	fetcher.Origin = cfg.Origin
	fetcher.InsecureTLS = cfg.InsecureTLS
	if normalized, nerr := crawler.Normalize(cfg.Seed, nil); nerr == nil {
		if u, uerr := url.Parse(normalized); uerr == nil {
			fetcher.OriginHost = u.Hostname()
		}
	}
	sink := &progressSink{st: st, crawlID: crawl.ID, stderr: stderr, quiet: *quiet}

	stats, runErr := crawler.Run(ctx, cfg, fetcher, sink)

	if serr := st.SetRobots(crawl.ID, stats.RobotsTxt); serr != nil {
		fmt.Fprintf(stderr, "error: guardando robots.txt: %v\n", serr)
	}

	status := "done"
	errMsg := ""
	switch {
	case errors.Is(runErr, context.Canceled):
		status = "cancelled"
	case runErr != nil:
		status = "failed"
		errMsg = runErr.Error()
	}
	if ferr := st.FinishCrawl(crawl.ID, status, errMsg); ferr != nil {
		fmt.Fprintf(stderr, "error: finalizando rastreo: %v\n", ferr)
	}

	finished, err := st.GetCrawl(crawl.ID)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	summary, err := st.Summarize(crawl.ID)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	broken, err := st.BrokenLinks(crawl.ID)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	if *jsonOutput {
		printCrawlJSON(stdout, finished, summary, stats)
	} else {
		printCrawlSummary(stdout, finished, summary, stats, len(broken))
	}

	switch status {
	case "cancelled":
		return 130
	case "failed":
		return 1
	default:
		return 0
	}
}

// progressSink adapts crawler.Sink to store.AddPage, converting types as it
// goes (the same conversion server/manager.go does, duplicated here since
// it is unexported there and cmd/eanbot must not modify server/). A page
// that fails to persist is logged and skipped rather than aborting the
// crawl. Unless quiet, it also writes one progress line per page to stderr.
type progressSink struct {
	st      *store.Store
	crawlID int64
	stderr  io.Writer
	quiet   bool
	idx     int
}

func (sk *progressSink) Page(p crawler.Page) {
	sk.idx++

	sp := store.Page{
		CrawlID:     sk.crawlID,
		URL:         p.URL,
		Depth:       p.Depth,
		Status:      p.Status,
		ContentType: p.ContentType,
		Size:        p.Size,
		DurationMs:  p.Duration.Milliseconds(),
		Title:       p.Title,
		Description: p.Description,
		Canonical:   p.Canonical,
		MetaRobots:  p.MetaRobots,
		NoIndex:     p.NoIndex,
		NoFollow:    p.NoFollow,
		H1:          p.H1,
		RedirectTo:  p.RedirectTo,
		Error:       p.Error,
		Blocked:     p.Blocked,
		FetchedAt:   p.FetchedAt,
	}

	links := make([]store.Link, 0, len(p.Links))
	for _, l := range p.Links {
		links = append(links, store.Link{
			ToURL:    l.URL,
			Text:     l.Text,
			NoFollow: l.NoFollow,
			InScope:  l.InScope,
		})
	}

	if _, err := sk.st.AddPage(sp, links); err != nil {
		fmt.Fprintf(sk.stderr, "error: guardando página %q: %v\n", p.URL, err)
	}

	if !sk.quiet {
		fmt.Fprintf(sk.stderr, "[%4d] %-4s %-11s %s\n", sk.idx, crawlerPageCode(p), p.ContentType, p.URL)
	}
}

// crawlerPageCode is the short status column used in progress lines: the
// numeric HTTP status, or BLOQ/ERR when no request was made or it failed.
func crawlerPageCode(p crawler.Page) string {
	switch {
	case p.Blocked:
		return "BLOQ"
	case p.Status == 0:
		return "ERR"
	default:
		return strconv.Itoa(p.Status)
	}
}

// crawlConfigDoc mirrors the unexported configDoc in server/manager.go
// (specs/004-api-web.md's "config" object) field for field, so that a crawl
// started from the CLI is displayed identically by the web UI. It is
// duplicated here, rather than imported, because cmd/eanbot must not modify
// server/ or depend on its unexported identifiers.
type crawlConfigDoc struct {
	Seed              string            `json:"seed"`
	MaxPages          int               `json:"max_pages"`
	MaxDepth          int               `json:"max_depth"`
	Concurrency       int               `json:"concurrency"`
	DelayMs           int               `json:"delay_ms"`
	TimeoutMs         int               `json:"timeout_ms"`
	MaxBodyBytes      int64             `json:"max_body_bytes"`
	UserAgent         string            `json:"user_agent"`
	IncludeSubdomains bool              `json:"include_subdomains"`
	IgnoreRobots      bool              `json:"ignore_robots"`
	UseSitemaps       bool              `json:"use_sitemaps"`
	Headers           map[string]string `json:"headers"`
	Origin            string            `json:"origin"`
	InsecureTLS       bool              `json:"insecure_tls"`
}

// crawlConfigJSON serializes cfg (already fully resolved from flags, no
// zero-value ambiguity) as the canonical "config" object.
func crawlConfigJSON(cfg crawler.Config) (json.RawMessage, error) {
	headers := cfg.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	doc := crawlConfigDoc{
		Seed:              cfg.Seed,
		MaxPages:          cfg.MaxPages,
		MaxDepth:          cfg.MaxDepth,
		Concurrency:       cfg.Concurrency,
		DelayMs:           int(cfg.Delay.Milliseconds()),
		TimeoutMs:         int(cfg.Timeout.Milliseconds()),
		MaxBodyBytes:      cfg.MaxBodyBytes,
		UserAgent:         cfg.UserAgent,
		IncludeSubdomains: cfg.IncludeSubdomains,
		IgnoreRobots:      cfg.IgnoreRobots,
		UseSitemaps:       cfg.UseSitemaps,
		Headers:           headers,
		Origin:            cfg.Origin,
		InsecureTLS:       cfg.InsecureTLS,
	}
	return json.Marshal(doc)
}

// printCrawlSummary writes the human-readable crawl summary, as fixed by
// specs/005-cli.md.
func printCrawlSummary(w io.Writer, c *store.Crawl, s *store.Summary, stats crawler.Stats, brokenCount int) {
	fmt.Fprintf(w, "Rastreo #%d · %s · %s\n", c.ID, c.Seed, c.Status)
	fmt.Fprintf(w, "Páginas: %d   2xx: %d   3xx: %d   4xx: %d   5xx: %d   Errores: %d   Bloqueadas: %d\n",
		s.Total, s.Status2xx, s.Status3xx, s.Status4xx, s.Status5xx, s.Errors, s.Blocked)
	fmt.Fprintf(w, "Noindex: %d   Profundidad máx.: %d   Media: %d ms   En cola sin visitar: %d\n",
		s.NoIndex, s.MaxDepth, s.AvgDurationMs, stats.Queued)
	if brokenCount > 0 {
		fmt.Fprintf(w, "Enlaces rotos: %d (ver: eanbot broken %d)\n", brokenCount, c.ID)
	} else {
		fmt.Fprintln(w, "Enlaces rotos: 0")
	}
}

// crawlJSONStats is the "stats" object in the -json crawl output.
type crawlJSONStats struct {
	Fetched  int `json:"fetched"`
	Blocked  int `json:"blocked"`
	Errors   int `json:"errors"`
	Queued   int `json:"queued"`
	Sitemaps int `json:"sitemaps"`
}

func printCrawlJSON(w io.Writer, c *store.Crawl, s *store.Summary, stats crawler.Stats) {
	out := struct {
		Crawl   *store.Crawl   `json:"crawl"`
		Summary *store.Summary `json:"summary"`
		Stats   crawlJSONStats `json:"stats"`
	}{
		Crawl:   c,
		Summary: s,
		Stats: crawlJSONStats{
			Fetched:  stats.Fetched,
			Blocked:  stats.Blocked,
			Errors:   stats.Errors,
			Queued:   stats.Queued,
			Sitemaps: stats.Sitemaps,
		},
	}
	b, err := json.Marshal(out)
	if err != nil {
		// json.Marshal on these concrete types cannot fail in practice.
		fmt.Fprintf(w, "error: %v\n", err)
		return
	}
	w.Write(b)
	fmt.Fprintln(w)
}
