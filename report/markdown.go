package report

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// sectionHeadings lists the "##" sections of the Markdown document, in the
// order fixed by specs/008-informe.md.
var sectionHeadings = []string{
	"Códigos",
	"Por profundidad",
	"Por idioma",
	"Por sección (top 50)",
	"Tipos de contenido",
	"Redirecciones",
	"Errores",
	"Páginas rotas con más referrers",
	"Títulos duplicados",
	"Páginas más lentas",
	"Páginas más grandes",
	"Muestras",
}

// sampleHeadings labels each Samples key in the "Muestras" section, in the
// order they are printed.
var sampleHeadings = []struct{ key, label string }{
	{"3xx", "Redirecciones (3xx)"},
	{"4xx", "No encontradas (4xx)"},
	{"5xx", "Errores de servidor (5xx)"},
	{"error", "Fallos de red"},
	{"blocked", "Bloqueadas por robots"},
	{"noindex", "noindex"},
	{"via_nofollow", "Solo vía nofollow"},
}

// noData is what an empty section prints instead of an empty table.
const noData = "Sin datos."

// Markdown renders a Report as a Markdown document. It is deterministic:
// every map is walked through an ordered slice, so the same report always
// produces the same bytes (which is what makes the snapshot test, and
// diffing two reports of the same site, worth anything).
func Markdown(r *Report) string {
	var b strings.Builder

	writeHeader(&b, r)

	writeSection(&b, "Códigos", func() {
		if len(r.ByStatus) == 0 {
			b.WriteString(noData + "\n")
			return
		}
		writeRow(&b, "Código", "Páginas")
		writeSep(&b, 2)
		for _, sc := range r.ByStatus {
			writeRow(&b, sc.Status, thousands(int64(sc.N)))
		}
	})

	writeBucketSection(&b, "Por profundidad", "Profundidad", r.ByDepth)
	writeBucketSection(&b, "Por idioma", "Idioma", r.ByLanguage)
	writeBucketSection(&b, "Por sección (top 50)", "Sección", r.BySection)

	writeSection(&b, "Tipos de contenido", func() {
		if len(r.ContentTypes) == 0 {
			b.WriteString(noData + "\n")
			return
		}
		types := make([]string, 0, len(r.ContentTypes))
		for ct := range r.ContentTypes {
			types = append(types, ct)
		}
		sort.Slice(types, func(i, j int) bool {
			if r.ContentTypes[types[i]] != r.ContentTypes[types[j]] {
				return r.ContentTypes[types[i]] > r.ContentTypes[types[j]]
			}
			return types[i] < types[j]
		})
		writeRow(&b, "Tipo", "Páginas")
		writeSep(&b, 2)
		for _, ct := range types {
			writeRow(&b, ct, thousands(int64(r.ContentTypes[ct])))
		}
	})

	writeSection(&b, "Redirecciones", func() {
		if len(r.Redirects) == 0 {
			b.WriteString(noData + "\n")
			return
		}
		writeRow(&b, "Patrón", "Páginas", "Ejemplo de origen", "Ejemplo de destino")
		writeSep(&b, 4)
		for _, rp := range r.Redirects {
			writeRow(&b, rp.Pattern, thousands(int64(rp.N)), rp.From, rp.To)
		}
	})

	writeSection(&b, "Errores", func() {
		if len(r.Errors) == 0 {
			b.WriteString(noData + "\n")
			return
		}
		writeRow(&b, "Tipo", "Páginas", "URL de ejemplo")
		writeSep(&b, 3)
		for _, ek := range r.Errors {
			writeRow(&b, ek.Kind, thousands(int64(ek.N)), ek.Sample)
		}
	})

	writeSection(&b, "Páginas rotas con más referrers", func() {
		if len(r.TopBroken) == 0 {
			b.WriteString(noData + "\n")
			return
		}
		fmt.Fprintf(&b, "Sobre %s páginas rotas analizadas.\n\n", thousands(int64(r.TopBrokenScanned)))
		writeRow(&b, "Referrers", "Código", "URL", "Error")
		writeSep(&b, 4)
		for _, br := range r.TopBroken {
			writeRow(&b, thousands(int64(br.Referrers)), codeLabel(br.Status, br.Error), br.URL, br.Error)
		}
	})

	writeSection(&b, "Títulos duplicados", func() {
		if len(r.DuplicateTitles) == 0 {
			b.WriteString(noData + "\n")
			return
		}
		writeRow(&b, "Páginas", "Título", "URL de ejemplo")
		writeSep(&b, 3)
		for _, tg := range r.DuplicateTitles {
			writeRow(&b, thousands(int64(tg.N)), tg.Title, tg.Sample)
		}
	})

	writeSection(&b, "Páginas más lentas", func() {
		if len(r.Slowest) == 0 {
			b.WriteString(noData + "\n")
			return
		}
		writeRow(&b, "Tiempo (ms)", "Código", "Prof.", "URL")
		writeSep(&b, 4)
		for _, p := range r.Slowest {
			writeRow(&b, thousands(p.DurationMs), codeLabel(p.Status, p.Extra), strconv.Itoa(p.Depth), p.URL)
		}
	})

	writeSection(&b, "Páginas más grandes", func() {
		if len(r.Largest) == 0 {
			b.WriteString(noData + "\n")
			return
		}
		writeRow(&b, "Tamaño (bytes)", "Código", "Prof.", "URL")
		writeSep(&b, 4)
		for _, p := range r.Largest {
			writeRow(&b, thousands(p.Size), codeLabel(p.Status, p.Extra), strconv.Itoa(p.Depth), p.URL)
		}
	})

	writeSection(&b, "Muestras", func() {
		written := false
		for _, sh := range sampleHeadings {
			refs := r.Samples[sh.key]
			if len(refs) == 0 {
				continue
			}
			if written {
				b.WriteString("\n")
			}
			written = true
			fmt.Fprintf(&b, "### %s\n\n", sh.label)
			writeRow(&b, "Código", "Prof.", "URL", "Detalle")
			writeSep(&b, 4)
			for _, p := range refs {
				writeRow(&b, codeLabel(p.Status, p.Extra), strconv.Itoa(p.Depth), p.URL, p.Extra)
			}
		}
		if !written {
			b.WriteString(noData + "\n")
		}
	})

	return b.String()
}

// writeHeader writes the title, the status/dates/config line and the
// summary table that open the document, before the first "##" section.
func writeHeader(b *strings.Builder, r *Report) {
	fmt.Fprintf(b, "# Informe del rastreo #%d — %s\n\n", r.Crawl.ID, r.Crawl.Seed)
	fmt.Fprintf(b, "Estado: %s · Inicio: %s · Fin: %s · Duración: %s\n",
		r.Crawl.Status, formatTime(r.Crawl.StartedAt), formatEndTime(r.Crawl.FinishedAt), crawlDuration(r.Crawl.StartedAt, r.Crawl.FinishedAt))
	fmt.Fprintf(b, "Generado: %s\n", formatTime(r.GeneratedAt))
	fmt.Fprintf(b, "Configuración: %s\n\n", configSummary(r.Crawl.Config))
	if r.Crawl.Error != "" {
		fmt.Fprintf(b, "Error del rastreo: %s\n\n", cell(r.Crawl.Error))
	}

	s := r.Summary
	writeRow(b, "Métrica", "Valor")
	writeSep(b, 2)
	writeRow(b, "Páginas", thousands(int64(s.Total)))
	writeRow(b, "2xx", thousands(int64(s.Status2xx)))
	writeRow(b, "3xx", thousands(int64(s.Status3xx)))
	writeRow(b, "4xx", thousands(int64(s.Status4xx)))
	writeRow(b, "5xx", thousands(int64(s.Status5xx)))
	writeRow(b, "Errores", thousands(int64(s.Errors)))
	writeRow(b, "Bloqueadas", thousands(int64(s.Blocked)))
	writeRow(b, "noindex", thousands(int64(s.NoIndex)))
	if configFollowsNoFollow(r.Crawl.Config) {
		writeRow(b, "Solo vía nofollow", thousands(int64(s.ViaNoFollow)))
	}
	writeRow(b, "Profundidad máxima", thousands(int64(s.MaxDepth)))
	writeRow(b, "Tiempo medio de respuesta", thousands(s.AvgDurationMs)+" ms")
}

// writeSection writes a "##" heading and delegates its body to body.
func writeSection(b *strings.Builder, heading string, body func()) {
	fmt.Fprintf(b, "\n## %s\n\n", heading)
	body()
}

// writeBucketSection writes one of the three identical Bucket breakdowns.
func writeBucketSection(b *strings.Builder, heading, keyHeading string, buckets []Bucket) {
	writeSection(b, heading, func() {
		if len(buckets) == 0 {
			b.WriteString(noData + "\n")
			return
		}
		writeRow(b, keyHeading, "Páginas", "2xx", "3xx", "4xx", "5xx", "Errores", "Bloq.", "noindex", "Media (ms)")
		writeSep(b, 10)
		for _, bk := range buckets {
			writeRow(b, bk.Key,
				thousands(int64(bk.Total)), thousands(int64(bk.S2xx)), thousands(int64(bk.S3xx)),
				thousands(int64(bk.S4xx)), thousands(int64(bk.S5xx)), thousands(int64(bk.Errors)),
				thousands(int64(bk.Blocked)), thousands(int64(bk.NoIndex)), thousands(bk.AvgMs))
		}
	})
}

func writeRow(b *strings.Builder, cells ...string) {
	b.WriteString("|")
	for _, c := range cells {
		b.WriteString(" " + cell(c) + " |")
	}
	b.WriteString("\n")
}

func writeSep(b *strings.Builder, n int) {
	b.WriteString("|")
	for i := 0; i < n; i++ {
		b.WriteString("---|")
	}
	b.WriteString("\n")
}

// cell makes a value safe inside a Markdown table: pipes are escaped and
// newlines flattened, so a stray title or error message cannot break the
// table. URLs are otherwise left exactly as crawled.
func cell(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.ReplaceAll(s, "|", `\|`)
}

// codeLabel is the status column: the numeric code, or BLOQ/ERR when there
// was no response (a page with an error message failed, one without it was
// blocked).
func codeLabel(status int, errMsg string) string {
	switch {
	case status > 0:
		return strconv.Itoa(status)
	case errMsg != "":
		return "ERR"
	default:
		return "BLOQ"
	}
}

// thousands formats an integer with "." as the thousands separator, as used
// throughout the Spanish-language report.
func thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	if len(s) <= 3 {
		return sign + s
	}

	var b strings.Builder
	lead := len(s) % 3
	if lead == 0 {
		lead = 3
	}
	b.WriteString(s[:lead])
	for i := lead; i < len(s); i += 3 {
		b.WriteString(".")
		b.WriteString(s[i : i+3])
	}
	return sign + b.String()
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

func formatEndTime(t *time.Time) string {
	if t == nil {
		return "(en curso)"
	}
	return formatTime(*t)
}

func crawlDuration(start time.Time, end *time.Time) string {
	if end == nil || start.IsZero() {
		return "—"
	}
	d := end.Sub(start).Round(time.Second)
	if d < 0 {
		return "—"
	}
	return d.String()
}

// configDoc is the subset of the stored crawl config the report summarizes.
// It mirrors the JSON shape fixed by specs/004-api-web.md; report keeps its
// own copy rather than importing server (which imports report's callers).
type configDoc struct {
	MaxPages          int    `json:"max_pages"`
	MaxDepth          int    `json:"max_depth"`
	Concurrency       int    `json:"concurrency"`
	DelayMs           *int   `json:"delay_ms"`
	TimeoutMs         int    `json:"timeout_ms"`
	UserAgent         string `json:"user_agent"`
	IncludeSubdomains bool   `json:"include_subdomains"`
	IgnoreRobots      bool   `json:"ignore_robots"`
	UseSitemaps       *bool  `json:"use_sitemaps"`
	FollowNoFollow    bool   `json:"follow_nofollow"`
}

// configFollowsNoFollow reports whether the stored crawl config had
// follow_nofollow set, which gates the "Solo vía nofollow" summary row.
func configFollowsNoFollow(raw json.RawMessage) bool {
	var doc configDoc
	if len(raw) == 0 || json.Unmarshal(raw, &doc) != nil {
		return false
	}
	return doc.FollowNoFollow
}

// configSummary condenses the stored crawl config into one line.
func configSummary(raw json.RawMessage) string {
	var doc configDoc
	if len(raw) == 0 || json.Unmarshal(raw, &doc) != nil {
		return "(no disponible)"
	}

	parts := []string{
		"máx. páginas " + thousands(int64(doc.MaxPages)),
		"máx. profundidad " + thousands(int64(doc.MaxDepth)),
		"concurrencia " + thousands(int64(doc.Concurrency)),
	}
	if doc.DelayMs != nil {
		parts = append(parts, "retardo "+durationLabel(int64(*doc.DelayMs)))
	}
	if doc.TimeoutMs > 0 {
		parts = append(parts, "timeout "+durationLabel(int64(doc.TimeoutMs)))
	}
	parts = append(parts, "subdominios: "+yesNo(doc.IncludeSubdomains))
	parts = append(parts, "robots: "+yesNo(!doc.IgnoreRobots))
	if doc.UseSitemaps != nil {
		parts = append(parts, "sitemaps: "+yesNo(*doc.UseSitemaps))
	}
	if doc.UserAgent != "" {
		parts = append(parts, "UA: "+doc.UserAgent)
	}
	return strings.Join(parts, " · ")
}

func durationLabel(ms int64) string {
	return (time.Duration(ms) * time.Millisecond).String()
}

func yesNo(b bool) string {
	if b {
		return "sí"
	}
	return "no"
}
