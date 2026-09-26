package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"xavi.net/eanbot/store"
)

// pageSpec is one seeded page; the zero value is a 200 text/html page.
type pageSpec struct {
	url        string
	depth      int
	status     int
	ct         string
	size       int64
	durMs      int64
	title      string
	redirectTo string
	errMsg     string
	blocked    bool
	noindex    bool
	links      []string
}

// seedPages covers every bucket the report distinguishes: two languages
// plus "(sin idioma)", several sections (including a percent-encoded one),
// depths 0-3, one 3xx per redirect pattern (with three pages sharing
// "segmento 2: a → b"), five error kinds, a blocked page, a noindex page,
// two duplicate title groups and three broken pages with 3/2/1 referrers.
var seedPages = []pageSpec{
	{url: "https://example.com/", depth: 0, durMs: 100, size: 1000, title: "Inicio",
		links: []string{"https://example.com/roto-404", "https://example.com/roto-500", "https://example.com/roto-410"}},
	{url: "https://example.com/en/docs/a", depth: 1, durMs: 300, size: 2000, title: "Guía",
		links: []string{"https://example.com/roto-404", "https://example.com/roto-500"}},
	{url: "https://example.com/en/docs/b", depth: 1, durMs: 250, size: 2500, title: "  guía ",
		links: []string{"https://example.com/roto-404"}},
	{url: "https://example.com/es/docs/c", depth: 2, durMs: 50, size: 500, title: "Otra", noindex: true},
	{url: "https://example.com/es/blog/d", depth: 2, durMs: 900, size: 9000, title: "Otra"},
	{url: "https://example.com/es/w%C3%B6hner/e", depth: 3, durMs: 120, size: 800, title: "Eñe"},
	{url: "https://example.com/w%C3%B6hner/g", depth: 3, durMs: 130, size: 700, title: "Otra cosa"},

	{url: "https://example.com/en/docs", depth: 1, status: 301, durMs: 10, redirectTo: "https://example.com/en/docs/"},
	{url: "https://example.com/es/blog/x/", depth: 2, status: 301, durMs: 10, redirectTo: "https://example.com/es/blog/x"},
	{url: "http://example.com/en/docs/h", depth: 1, status: 301, durMs: 10, redirectTo: "https://example.com/en/docs/h"},
	{url: "https://example.com/en/docs/J", depth: 1, status: 301, durMs: 10, redirectTo: "https://example.com/en/docs/j"},
	{url: "https://example.com/en/docs/k?utm=1", depth: 1, status: 302, durMs: 10, redirectTo: "https://example.com/en/docs/k"},
	{url: "https://example.com/en/a/1", depth: 1, status: 301, durMs: 10, redirectTo: "https://example.com/en/b/1"},
	{url: "https://example.com/en/a/2", depth: 1, status: 301, durMs: 10, redirectTo: "https://example.com/en/b/2"},
	{url: "https://example.com/en/a/3", depth: 1, status: 301, durMs: 10, redirectTo: "https://example.com/en/b/3"},
	{url: "https://example.com/legal", depth: 1, status: 301, durMs: 10, redirectTo: "https://example.com/en/legal"},
	{url: "https://example.com/old", depth: 1, status: 301, durMs: 10, redirectTo: "https://example.com/nuevo/sitio"},
	{url: "https://example.com/ext", depth: 1, status: 302, durMs: 10, redirectTo: "https://otro.example.org/"},

	{url: "https://example.com/e/timeout", depth: 2, status: -1, errMsg: `Get "https://example.com/e/timeout": context deadline exceeded`},
	{url: "https://example.com/e/dns", depth: 2, status: -1, errMsg: "dial tcp: lookup nope.example.com: no such host"},
	{url: "https://example.com/e/refused", depth: 2, status: -1, errMsg: "dial tcp 127.0.0.1:1: connect: connection refused"},
	{url: "https://example.com/e/tls", depth: 2, status: -1, errMsg: "x509: certificate signed by unknown authority"},
	{url: "https://example.com/e/otro", depth: 2, status: -1, errMsg: `Get "https://example.com/e/otro": algo raro ha pasado`},
	{url: "https://example.com/privado", depth: 1, status: -1, blocked: true},

	{url: "https://example.com/roto-404", depth: 2, status: 404, durMs: 20},
	{url: "https://example.com/roto-500", depth: 2, status: 500, durMs: 20},
	{url: "https://example.com/roto-410", depth: 2, status: 410, durMs: 20},
}

// seedStore builds a store in t.TempDir() holding the seedPages crawl.
func seedStore(t *testing.T) (*store.Store, int64) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "eanbot.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := json.RawMessage(`{"seed":"https://example.com/","max_pages":500,"max_depth":10,"concurrency":4,"delay_ms":500,"timeout_ms":15000,"user_agent":"eanbot/0.1"}`)
	c, err := st.CreateCrawl("https://example.com/", cfg)
	if err != nil {
		t.Fatalf("CreateCrawl() error = %v", err)
	}

	batch := make([]store.PageWithLinks, 0, len(seedPages))
	fetchedAt := time.Date(2026, 9, 17, 9, 15, 0, 0, time.UTC)
	for _, ps := range seedPages {
		status := ps.status
		if status == 0 {
			status = 200
		} else if status == -1 {
			status = 0
		}
		ct := ps.ct
		if ct == "" && status >= 200 && status <= 299 {
			ct = "text/html"
		}
		p := store.Page{
			CrawlID: c.ID, URL: ps.url, Depth: ps.depth, Status: status, ContentType: ct,
			Size: ps.size, DurationMs: ps.durMs, Title: ps.title, RedirectTo: ps.redirectTo,
			Error: ps.errMsg, Blocked: ps.blocked, NoIndex: ps.noindex, FetchedAt: fetchedAt,
		}
		var links []store.Link
		for _, to := range ps.links {
			links = append(links, store.Link{ToURL: to, InScope: true})
		}
		batch = append(batch, store.PageWithLinks{Page: p, Links: links})
	}
	if _, err := st.AddPages(c.ID, batch); err != nil {
		t.Fatalf("AddPages() error = %v", err)
	}
	if err := st.FinishCrawl(c.ID, "done", ""); err != nil {
		t.Fatalf("FinishCrawl() error = %v", err)
	}
	return st, c.ID
}

// buildSeeded builds the report of the seeded crawl with a fixed sampling
// seed, so Samples (and the Markdown snapshot) are reproducible.
func buildSeeded(t *testing.T) *Report {
	t.Helper()
	old := seed
	seed = 20260917
	t.Cleanup(func() { seed = old })

	st, crawlID := seedStore(t)
	r, err := Build(context.Background(), st, crawlID)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return r
}

func bucketByKey(t *testing.T, bs []Bucket, key string) Bucket {
	t.Helper()
	for _, b := range bs {
		if b.Key == key {
			return b
		}
	}
	t.Fatalf("no bucket with key %q in %v", key, bs)
	return Bucket{}
}

func TestBuild_CrawlAndSummary(t *testing.T) {
	r := buildSeeded(t)

	if r.Crawl.Seed != "https://example.com/" {
		t.Errorf("Crawl.Seed = %q", r.Crawl.Seed)
	}
	if r.Crawl.Status != "done" {
		t.Errorf("Crawl.Status = %q, want done", r.Crawl.Status)
	}
	if r.GeneratedAt.IsZero() {
		t.Errorf("GeneratedAt is zero")
	}
	want := store.Summary{
		Total: 27, Status2xx: 7, Status3xx: 11, Status4xx: 2, Status5xx: 1,
		Errors: 5, Blocked: 1, NoIndex: 1, MaxDepth: 3,
	}
	got := r.Summary
	if got.Total != want.Total || got.Status2xx != want.Status2xx || got.Status3xx != want.Status3xx ||
		got.Status4xx != want.Status4xx || got.Status5xx != want.Status5xx || got.Errors != want.Errors ||
		got.Blocked != want.Blocked || got.NoIndex != want.NoIndex || got.MaxDepth != want.MaxDepth {
		t.Errorf("Summary = %+v, want %+v", got, want)
	}
	if r.ContentTypes["text/html"] != 7 {
		t.Errorf("ContentTypes = %v, want text/html: 7", r.ContentTypes)
	}
}

func TestBuild_ByDepth(t *testing.T) {
	r := buildSeeded(t)

	if len(r.ByDepth) != 4 {
		t.Fatalf("ByDepth has %d buckets, want 4: %+v", len(r.ByDepth), r.ByDepth)
	}
	for i, want := range []string{"0", "1", "2", "3"} {
		if r.ByDepth[i].Key != want {
			t.Fatalf("ByDepth[%d].Key = %q, want %q (ascending)", i, r.ByDepth[i].Key, want)
		}
	}
	tests := []struct {
		key  string
		want Bucket
	}{
		{"0", Bucket{Key: "0", Total: 1, S2xx: 1, AvgMs: 100}},
		{"1", Bucket{Key: "1", Total: 13, S2xx: 2, S3xx: 10, Blocked: 1, AvgMs: 54}},
		{"2", Bucket{Key: "2", Total: 11, S2xx: 2, S3xx: 1, S4xx: 2, S5xx: 1, Errors: 5, NoIndex: 1, AvgMs: 170}},
		{"3", Bucket{Key: "3", Total: 2, S2xx: 2, AvgMs: 125}},
	}
	for _, tc := range tests {
		if got := bucketByKey(t, r.ByDepth, tc.key); got != tc.want {
			t.Errorf("depth %s = %+v, want %+v", tc.key, got, tc.want)
		}
	}
}

func TestBuild_ByStatus(t *testing.T) {
	r := buildSeeded(t)

	got := map[string]int{}
	for _, sc := range r.ByStatus {
		got[sc.Status] = sc.N
	}
	want := map[string]int{"200": 7, "301": 9, "302": 2, "404": 1, "410": 1, "500": 1, "ERR": 5, "BLOQ": 1}
	if len(got) != len(want) {
		t.Fatalf("ByStatus = %+v, want %v", r.ByStatus, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("ByStatus[%s] = %d, want %d", k, got[k], v)
		}
	}
	for i := 1; i < len(r.ByStatus); i++ {
		if r.ByStatus[i-1].N < r.ByStatus[i].N {
			t.Fatalf("ByStatus not sorted by n desc: %+v", r.ByStatus)
		}
	}
}

func TestBuild_ByLanguage(t *testing.T) {
	r := buildSeeded(t)

	wantOrder := []string{noLanguage, "en", "es"}
	if len(r.ByLanguage) != len(wantOrder) {
		t.Fatalf("ByLanguage = %+v, want 3 buckets", r.ByLanguage)
	}
	for i, k := range wantOrder {
		if r.ByLanguage[i].Key != k {
			t.Fatalf("ByLanguage[%d].Key = %q, want %q (total desc)", i, r.ByLanguage[i].Key, k)
		}
	}
	if got := bucketByKey(t, r.ByLanguage, "en"); got.Total != 9 || got.S2xx != 2 || got.S3xx != 7 {
		t.Errorf("language en = %+v, want total 9, 2xx 2, 3xx 7", got)
	}
	if got := bucketByKey(t, r.ByLanguage, "es"); got.Total != 4 || got.S2xx != 3 || got.S3xx != 1 || got.NoIndex != 1 {
		t.Errorf("language es = %+v, want total 4, 2xx 3, 3xx 1, noindex 1", got)
	}
	if got := bucketByKey(t, r.ByLanguage, noLanguage); got.Total != 14 {
		t.Errorf("language %s total = %d, want 14", noLanguage, got.Total)
	}
}

func TestBuild_BySectionDecodesPercentEncoding(t *testing.T) {
	r := buildSeeded(t)

	if got := bucketByKey(t, r.BySection, "docs"); got.Total != 7 {
		t.Errorf("section docs total = %d, want 7", got.Total)
	}
	// w%C3%B6hner and wöhner must land in the same bucket.
	if got := bucketByKey(t, r.BySection, "wöhner"); got.Total != 2 {
		t.Errorf("section wöhner total = %d, want 2", got.Total)
	}
	if got := bucketByKey(t, r.BySection, rootSection); got.Total != 1 {
		t.Errorf("section %s total = %d, want 1", rootSection, got.Total)
	}
	if got := bucketByKey(t, r.BySection, "e"); got.Total != 5 {
		t.Errorf("section e total = %d, want 5", got.Total)
	}
	if r.BySection[0].Key != "docs" {
		t.Errorf("BySection[0] = %q, want docs (total desc)", r.BySection[0].Key)
	}
	total := 0
	for _, b := range r.BySection {
		total += b.Total
	}
	if total != 27 {
		t.Errorf("sections add up to %d pages, want 27", total)
	}
}

func TestSectionBuckets_TopFiftyPlusRest(t *testing.T) {
	acc := map[string]*bucketAcc{}
	for i := 0; i < 60; i++ {
		b := &bucketAcc{}
		// Sections 0..59 with descending totals, so the top 50 are 0..49.
		for j := 0; j <= 60-i; j++ {
			b.Total++
			b.S2xx++
		}
		acc[fmt.Sprintf("s%02d", i)] = b
	}

	got := sectionBuckets(acc)
	if len(got) != 51 {
		t.Fatalf("got %d buckets, want 50 + 1 aggregate", len(got))
	}
	if got[0].Key != "s00" {
		t.Errorf("first bucket = %q, want s00", got[0].Key)
	}
	last := got[len(got)-1]
	if last.Key != "(otras 10 secciones)" {
		t.Fatalf("last bucket = %q, want (otras 10 secciones)", last.Key)
	}
	wantRest := 0
	for i := 50; i < 60; i++ {
		wantRest += 60 - i + 1
	}
	if last.Total != wantRest || last.S2xx != wantRest {
		t.Errorf("aggregate bucket = %+v, want total %d", last, wantRest)
	}
}

func TestBuild_Redirects(t *testing.T) {
	r := buildSeeded(t)

	got := map[string]RedirectPattern{}
	for _, rp := range r.Redirects {
		got[rp.Pattern] = rp
	}
	want := map[string]int{
		"segmento 2: a → b": 3,
		"añade barra final": 1,
		"quita barra final": 1,
		"http→https":        1,
		"cambia mayúsculas": 1,
		"quita query":       1,
		"añade prefijo /en": 1,
		"otra ruta":         1,
		"otro host":         1,
	}
	if len(got) != len(want) {
		t.Fatalf("Redirects = %+v, want %d patterns", r.Redirects, len(want))
	}
	for pattern, n := range want {
		rp, ok := got[pattern]
		if !ok {
			t.Errorf("missing redirect pattern %q", pattern)
			continue
		}
		if rp.N != n {
			t.Errorf("pattern %q n = %d, want %d", pattern, rp.N, n)
		}
		if rp.From == "" || rp.To == "" {
			t.Errorf("pattern %q has empty sample: %+v", pattern, rp)
		}
	}
	if r.Redirects[0].Pattern != "segmento 2: a → b" {
		t.Errorf("Redirects[0] = %q, want the 3-page pattern first", r.Redirects[0].Pattern)
	}
	if rp := got["segmento 2: a → b"]; rp.From != "https://example.com/en/a/1" || rp.To != "https://example.com/en/b/1" {
		t.Errorf("segment sample = %q → %q, want the first page seen", rp.From, rp.To)
	}
}

func TestBuild_Errors(t *testing.T) {
	r := buildSeeded(t)

	got := map[string]ErrorKind{}
	for _, ek := range r.Errors {
		got[ek.Kind] = ek
	}
	for _, kind := range []string{"timeout", "dns", "conexión rechazada", "tls"} {
		ek, ok := got[kind]
		if !ok {
			t.Fatalf("missing error kind %q in %+v", kind, r.Errors)
		}
		if ek.N != 1 || ek.Sample == "" {
			t.Errorf("error kind %q = %+v, want n 1 and a sample", kind, ek)
		}
	}
	if len(r.Errors) != 5 {
		t.Fatalf("Errors = %+v, want 5 kinds", r.Errors)
	}
	var other ErrorKind
	for _, ek := range r.Errors {
		if len(ek.Kind) > 6 && ek.Kind[:6] == "otro: " {
			other = ek
		}
	}
	if other.N != 1 {
		t.Fatalf("no 'otro:' error kind in %+v", r.Errors)
	}
	if want := `otro: Get "": algo raro ha pasado`; other.Kind != want {
		t.Errorf("other kind = %q, want %q (URL removed)", other.Kind, want)
	}
}

func TestErrorKindFor(t *testing.T) {
	const url = "https://example.com/x"
	tests := []struct{ err, want string }{
		{"robots.txt: 500", "robots.txt"},
		{`Get "https://example.com/x": net/http: request canceled (Client.Timeout exceeded while awaiting headers)`, "timeout"},
		{"context deadline exceeded", "timeout"},
		{"dial tcp: connect: connection refused", "conexión rechazada"},
		{"tls: handshake failure", "tls"},
		{"x509: unknown authority", "tls"},
		{"unexpected EOF", "conexión cerrada"},
		{"read: connection reset by peer", "conexión cerrada"},
		{"lookup x: no such host", "dns"},
		{"body truncated at 10 MiB", "cuerpo"},
		{"error leyendo el body", "cuerpo"},
		{"algo https://example.com/x inesperado", "otro: algo inesperado"},
	}
	for _, tc := range tests {
		if got := errorKindFor(tc.err, url); got != tc.want {
			t.Errorf("errorKindFor(%q) = %q, want %q", tc.err, got, tc.want)
		}
	}

	long := "raro: " + string(make([]byte, 0)) + "0123456789012345678901234567890123456789012345678901234567890123456789"
	got := errorKindFor(long, url)
	if len([]rune(got)) != len("otro: ")+60 {
		t.Errorf("errorKindFor(long) = %q (%d runes), want the error truncated to 60", got, len([]rune(got)))
	}
}

func TestBuild_TopBroken(t *testing.T) {
	r := buildSeeded(t)

	if r.TopBrokenScanned != 8 {
		t.Errorf("TopBrokenScanned = %d, want 8", r.TopBrokenScanned)
	}
	if len(r.TopBroken) != 8 {
		t.Fatalf("TopBroken has %d entries, want 8: %+v", len(r.TopBroken), r.TopBroken)
	}
	want := []struct {
		url  string
		n    int
		code int
	}{
		{"https://example.com/roto-404", 3, 404},
		{"https://example.com/roto-500", 2, 500},
		{"https://example.com/roto-410", 1, 410},
	}
	for i, w := range want {
		got := r.TopBroken[i]
		if got.URL != w.url || got.Referrers != w.n || got.Status != w.code {
			t.Errorf("TopBroken[%d] = %+v, want %s (%d refs, %d)", i, got, w.url, w.n, w.code)
		}
	}
	// Ties (0 referrers) are broken by URL.
	if r.TopBroken[3].URL != "https://example.com/e/dns" {
		t.Errorf("TopBroken[3] = %q, want the first 0-referrer URL alphabetically", r.TopBroken[3].URL)
	}
	if r.TopBroken[3].Error == "" {
		t.Errorf("TopBroken[3] has no Error text: %+v", r.TopBroken[3])
	}
}

func TestBuild_DuplicateTitles(t *testing.T) {
	r := buildSeeded(t)

	if len(r.DuplicateTitles) != 2 {
		t.Fatalf("DuplicateTitles = %+v, want 2 groups", r.DuplicateTitles)
	}
	if got := r.DuplicateTitles[0]; got.Title != "Otra" || got.N != 2 || got.Sample != "https://example.com/es/blog/d" {
		t.Errorf("DuplicateTitles[0] = %+v, want Otra/2", got)
	}
	if got := r.DuplicateTitles[1]; got.Title != "guía" || got.N != 2 {
		t.Errorf("DuplicateTitles[1] = %+v, want guía/2 (case and spacing normalized for grouping)", got)
	}
}

func TestBuild_SlowestAndLargest(t *testing.T) {
	r := buildSeeded(t)

	if len(r.Slowest) != 10 {
		t.Fatalf("Slowest has %d entries, want 10", len(r.Slowest))
	}
	if r.Slowest[0].URL != "https://example.com/es/blog/d" || r.Slowest[0].DurationMs != 900 {
		t.Errorf("Slowest[0] = %+v, want /es/blog/d 900ms", r.Slowest[0])
	}
	for i := 1; i < len(r.Slowest); i++ {
		if r.Slowest[i-1].DurationMs < r.Slowest[i].DurationMs {
			t.Fatalf("Slowest not descending: %+v", r.Slowest)
		}
	}
	if len(r.Largest) != 10 {
		t.Fatalf("Largest has %d entries, want 10", len(r.Largest))
	}
	if r.Largest[0].URL != "https://example.com/es/blog/d" || r.Largest[0].Size != 9000 {
		t.Errorf("Largest[0] = %+v, want /es/blog/d 9000", r.Largest[0])
	}
	if r.Largest[0].Depth != 2 {
		t.Errorf("Largest[0].Depth = %d, want 2", r.Largest[0].Depth)
	}
	for i := 1; i < len(r.Largest); i++ {
		if r.Largest[i-1].Size < r.Largest[i].Size {
			t.Fatalf("Largest not descending: %+v", r.Largest)
		}
	}
}

func TestBuild_Samples(t *testing.T) {
	r := buildSeeded(t)

	wantLen := map[string]int{"3xx": 10, "4xx": 2, "5xx": 1, "error": 5, "blocked": 1, "noindex": 1}
	for key, n := range wantLen {
		got, ok := r.Samples[key]
		if !ok {
			t.Fatalf("Samples has no %q key: %v", key, r.Samples)
		}
		if len(got) != n {
			t.Errorf("Samples[%q] has %d entries, want %d", key, len(got), n)
		}
		if len(got) > samplesPerKey {
			t.Errorf("Samples[%q] has %d entries, over the cap of %d", key, len(got), samplesPerKey)
		}
	}
	if len(r.Samples) != len(wantLen) {
		t.Errorf("Samples keys = %d, want %d", len(r.Samples), len(wantLen))
	}
	if got := r.Samples["blocked"][0].URL; got != "https://example.com/privado" {
		t.Errorf("Samples[blocked] = %q, want the blocked page", got)
	}
	if got := r.Samples["noindex"][0].URL; got != "https://example.com/es/docs/c" {
		t.Errorf("Samples[noindex] = %q, want the noindex page", got)
	}
}

func TestBuild_SamplesAreDeterministicForAFixedSeed(t *testing.T) {
	first := buildSeeded(t)
	second := buildSeeded(t)

	for i := range first.Samples["3xx"] {
		if first.Samples["3xx"][i].URL != second.Samples["3xx"][i].URL {
			t.Fatalf("sample %d differs between runs with the same seed: %q vs %q",
				i, first.Samples["3xx"][i].URL, second.Samples["3xx"][i].URL)
		}
	}
}

// TestBuild_ViaNoFollowSample checks that a page marked ViaNoFollow lands in
// the "via_nofollow" Samples bucket, and that a crawl with no such page gets
// no such key at all (exercised by TestBuild_Samples, which asserts the
// exact set of keys on the base seeded crawl).
func TestBuild_ViaNoFollowSample(t *testing.T) {
	old := seed
	seed = 20260917
	t.Cleanup(func() { seed = old })

	st, err := store.Open(filepath.Join(t.TempDir(), "eanbot.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := json.RawMessage(`{"seed":"https://example.com/","follow_nofollow":true}`)
	c, err := st.CreateCrawl("https://example.com/", cfg)
	if err != nil {
		t.Fatalf("CreateCrawl() error = %v", err)
	}
	fetchedAt := time.Date(2026, 9, 17, 9, 15, 0, 0, time.UTC)
	batch := []store.PageWithLinks{
		{Page: store.Page{CrawlID: c.ID, URL: "https://example.com/", Status: 200, ContentType: "text/html", FetchedAt: fetchedAt}},
		{Page: store.Page{CrawlID: c.ID, URL: "https://example.com/hidden", Status: 200, ContentType: "text/html", ViaNoFollow: true, FetchedAt: fetchedAt}},
	}
	if _, err := st.AddPages(c.ID, batch); err != nil {
		t.Fatalf("AddPages() error = %v", err)
	}
	if err := st.FinishCrawl(c.ID, "done", ""); err != nil {
		t.Fatalf("FinishCrawl() error = %v", err)
	}

	r, err := Build(context.Background(), st, c.ID)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if r.Summary.ViaNoFollow != 1 {
		t.Errorf("Summary.ViaNoFollow = %d, want 1", r.Summary.ViaNoFollow)
	}
	got, ok := r.Samples["via_nofollow"]
	if !ok || len(got) != 1 || got[0].URL != "https://example.com/hidden" {
		t.Errorf("Samples[via_nofollow] = %v, want a single entry for /hidden", got)
	}
}

func TestBuild_NotFound(t *testing.T) {
	st, _ := seedStore(t)
	if _, err := Build(context.Background(), st, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Build() error = %v, want store.ErrNotFound", err)
	}
}

func TestBuild_ContextCancelled(t *testing.T) {
	st, crawlID := seedStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Build(ctx, st, crawlID); !errors.Is(err, context.Canceled) {
		t.Fatalf("Build() error = %v, want context.Canceled", err)
	}
}
