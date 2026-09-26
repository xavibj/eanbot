// Package report turns a stored crawl into a single aggregate report:
// status codes by depth, language and section, redirect patterns, grouped
// errors, the broken pages with the most referrers, duplicate titles, the
// slowest and largest pages and a random sample of each problematic group.
// See specs/008-informe.md.
//
// It depends only on store (plus the standard library): it is built on a
// single streaming pass over the crawl's pages, so a crawl of a million
// pages is summarized in seconds with bounded memory, and it knows nothing
// about server, crawler or the CLI.
package report

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"xavi.net/eanbot/store"
)

// Report is the whole aggregate report of one crawl.
type Report struct {
	GeneratedAt      time.Time            `json:"generated_at"`
	Crawl            store.Crawl          `json:"crawl"`
	Summary          store.Summary        `json:"summary"`
	ByDepth          []Bucket             `json:"by_depth"`
	ByStatus         []StatusCount        `json:"by_status"`
	ByLanguage       []Bucket             `json:"by_language"`
	BySection        []Bucket             `json:"by_section"`
	ContentTypes     map[string]int       `json:"content_types"`
	Redirects        []RedirectPattern    `json:"redirects"`
	Errors           []ErrorKind          `json:"errors"`
	TopBroken        []BrokenRanked       `json:"top_broken"`
	TopBrokenScanned int                  `json:"top_broken_scanned"`
	DuplicateTitles  []TitleGroup         `json:"duplicate_titles"`
	Slowest          []PageRef            `json:"slowest"`
	Largest          []PageRef            `json:"largest"`
	Samples          map[string][]PageRef `json:"samples"`
}

// Bucket is one row of a per-depth, per-language or per-section breakdown.
type Bucket struct {
	Key     string `json:"key"`
	Total   int    `json:"total"`
	S2xx    int    `json:"status_2xx"`
	S3xx    int    `json:"status_3xx"`
	S4xx    int    `json:"status_4xx"`
	S5xx    int    `json:"status_5xx"`
	Errors  int    `json:"errors"`
	Blocked int    `json:"blocked"`
	NoIndex int    `json:"noindex"`
	AvgMs   int64  `json:"avg_ms"`
}

// StatusCount counts the pages with one exact status code ("404"), or the
// pseudo-codes "ERR" (a fetch that failed) and "BLOQ" (blocked by robots).
type StatusCount struct {
	Status string `json:"status"`
	N      int    `json:"n"`
}

// RedirectPattern counts the 3xx pages whose redirect follows one shape
// ("añade barra final", "segmento 2: es → en"...), with one example.
type RedirectPattern struct {
	Pattern string `json:"pattern"`
	N       int    `json:"n"`
	From    string `json:"sample_from"`
	To      string `json:"sample_to"`
}

// ErrorKind groups failed fetches by the kind of failure.
type ErrorKind struct {
	Kind   string `json:"kind"`
	N      int    `json:"n"`
	Sample string `json:"sample_url"`
}

// BrokenRanked is a broken page ranked by how many pages link to it.
type BrokenRanked struct {
	URL       string `json:"url"`
	Status    int    `json:"status"`
	Error     string `json:"error"`
	Referrers int    `json:"referrers_count"`
}

// TitleGroup is a group of pages sharing the same (normalized) title.
type TitleGroup struct {
	Title  string `json:"title"`
	N      int    `json:"n"`
	Sample string `json:"sample_url"`
}

// PageRef is the compact page reference used by the top-N lists and the
// random samples. Extra carries whatever is worth showing next to the URL:
// the redirect target of a 3xx, the message of a failed fetch, or the meta
// robots value of a noindex page.
type PageRef struct {
	URL        string `json:"url"`
	Status     int    `json:"status"`
	DurationMs int64  `json:"duration_ms"`
	Size       int64  `json:"size"`
	Depth      int    `json:"depth"`
	Extra      string `json:"extra,omitempty"`
}

const (
	// noLanguage is the bucket for URLs whose first path segment is not a
	// two-letter language code.
	noLanguage = "(sin idioma)"
	// rootSection is the bucket for URLs with no section segment.
	rootSection = "(raíz)"

	// samplesPerKey is how many random pages each Samples key holds.
	samplesPerKey = 10
	// maxSections is how many sections BySection lists individually before
	// aggregating the rest into a single "(otras N secciones)" row.
	maxSections = 50
	// maxRedirectPatterns, maxErrorKinds, maxBroken, maxTitleGroups and
	// maxTopPages cap the remaining top-N lists, per specs/008-informe.md.
	maxRedirectPatterns = 30
	maxErrorKinds       = 20
	maxBroken           = 20
	maxTitleGroups      = 20
	maxTopPages         = 10

	// brokenScanLimit is how many broken pages TopBroken ranks. Ranking by
	// referrer count needs the counts of every candidate, so the window is
	// bounded here rather than in the store: 5000 rows is a cheap query and
	// well past the point where the top 20 could still change in practice.
	brokenScanLimit = 5000
)

// seed seeds the reservoir sampling of Samples. It defaults to the current
// time (every report gets a different, unbiased sample) and is a package
// variable purely so tests can pin it and get reproducible samples.
var seed int64 = time.Now().UnixNano()

// Build walks every page of a crawl once and returns its aggregate report.
// It returns store.ErrNotFound if the crawl does not exist, or ctx's error
// if ctx is cancelled during the pass.
func Build(ctx context.Context, st *store.Store, crawlID int64) (*Report, error) {
	crawl, err := st.GetCrawl(crawlID)
	if err != nil {
		return nil, err
	}
	summary, err := st.Summarize(crawlID)
	if err != nil {
		return nil, err
	}

	acc := newAccumulator()
	if err := st.ForEachPage(ctx, crawlID, acc.add); err != nil {
		return nil, err
	}

	r := &Report{
		GeneratedAt:  time.Now().UTC(),
		Crawl:        *crawl,
		Summary:      *summary,
		ContentTypes: summary.ContentTypes,
	}
	acc.finish(r)

	broken, _, err := st.BrokenReferrerCounts(crawlID, brokenScanLimit)
	if err != nil {
		return nil, err
	}
	r.TopBrokenScanned = len(broken)
	r.TopBroken = rankBroken(broken)

	return r, nil
}

// --- the single pass ---

// bucketAcc accumulates one Bucket plus the running average it needs.
type bucketAcc struct {
	Bucket
	durSum int64
	durN   int64
}

func (b *bucketAcc) add(p store.Page) {
	b.Total++
	switch {
	case p.Status >= 200 && p.Status <= 299:
		b.S2xx++
	case p.Status >= 300 && p.Status <= 399:
		b.S3xx++
	case p.Status >= 400 && p.Status <= 499:
		b.S4xx++
	case p.Status >= 500 && p.Status <= 599:
		b.S5xx++
	case p.Status == 0 && !p.Blocked:
		b.Errors++
	}
	if p.Blocked {
		b.Blocked++
	}
	if p.NoIndex {
		b.NoIndex++
	}
	if p.Status > 0 {
		b.durSum += p.DurationMs
		b.durN++
	}
}

// bucket finalizes the accumulator into a Bucket with the given key.
func (b *bucketAcc) bucket(key string) Bucket {
	out := b.Bucket
	out.Key = key
	if b.durN > 0 {
		out.AvgMs = b.durSum / b.durN
	}
	return out
}

// mergeInto folds this accumulator into another one (used by the
// "(otras N secciones)" aggregate row).
func (b *bucketAcc) mergeInto(dst *bucketAcc) {
	dst.Total += b.Total
	dst.S2xx += b.S2xx
	dst.S3xx += b.S3xx
	dst.S4xx += b.S4xx
	dst.S5xx += b.S5xx
	dst.Errors += b.Errors
	dst.Blocked += b.Blocked
	dst.NoIndex += b.NoIndex
	dst.durSum += b.durSum
	dst.durN += b.durN
}

// titleAcc is the sample kept for a duplicate-title group. Only groups that
// reach two pages keep their text and a sample URL; the rest cost one map
// entry with a 64-bit key, which is what keeps a million-page crawl's title
// counting bounded (see "Cómo se calcula" in specs/008-informe.md).
type titleAcc struct {
	title  string
	sample string
}

// accumulator holds every running total of the single pass.
type accumulator struct {
	depths    map[int]*bucketAcc
	statuses  map[string]int
	languages map[string]*bucketAcc
	sections  map[string]*bucketAcc
	redirects map[string]*RedirectPattern
	errors    map[string]*ErrorKind

	titleCounts map[uint64]int
	titleInfo   map[uint64]*titleAcc

	slowest topPages
	largest topPages

	sampler *sampler
}

func newAccumulator() *accumulator {
	return &accumulator{
		depths:      map[int]*bucketAcc{},
		statuses:    map[string]int{},
		languages:   map[string]*bucketAcc{},
		sections:    map[string]*bucketAcc{},
		redirects:   map[string]*RedirectPattern{},
		errors:      map[string]*ErrorKind{},
		titleCounts: map[uint64]int{},
		titleInfo:   map[uint64]*titleAcc{},
		slowest:     topPages{n: maxTopPages, better: slowerFirst},
		largest:     topPages{n: maxTopPages, better: biggerFirst},
		sampler:     newSampler(seed),
	}
}

// add folds one page into every breakdown. It is the callback of
// store.ForEachPage and is therefore the only place that touches a page.
func (a *accumulator) add(p store.Page) error {
	bucketFor(a.depths, p.Depth).add(p)
	a.statuses[statusKey(p)]++

	lang, section := languageAndSection(p.URL)
	bucketFor(a.languages, lang).add(p)
	bucketFor(a.sections, section).add(p)

	if p.Status >= 300 && p.Status <= 399 && p.RedirectTo != "" {
		a.addRedirect(p)
	}
	if p.Status == 0 && !p.Blocked {
		a.addError(p)
	}
	a.addTitle(p)

	ref := pageRef(p)
	if p.Status > 0 {
		a.slowest.offer(ref)
		a.largest.offer(ref)
	}
	for _, key := range sampleKeys(p) {
		a.sampler.offer(key, ref)
	}
	return nil
}

func (a *accumulator) addRedirect(p store.Page) {
	pattern := redirectPattern(p.URL, p.RedirectTo)
	rp, ok := a.redirects[pattern]
	if !ok {
		rp = &RedirectPattern{Pattern: pattern, From: p.URL, To: p.RedirectTo}
		a.redirects[pattern] = rp
	}
	rp.N++
}

func (a *accumulator) addError(p store.Page) {
	kind := errorKindFor(p.Error, p.URL)
	ek, ok := a.errors[kind]
	if !ok {
		ek = &ErrorKind{Kind: kind, Sample: p.URL}
		a.errors[kind] = ek
	}
	ek.N++
}

func (a *accumulator) addTitle(p store.Page) {
	if p.Title == "" || p.Status < 200 || p.Status > 299 || !strings.HasPrefix(p.ContentType, "text/html") {
		return
	}
	normalized := normalizeTitle(p.Title)
	if normalized == "" {
		return
	}
	h := fnv.New64a()
	h.Write([]byte(normalized))
	key := h.Sum64()

	a.titleCounts[key]++
	if a.titleCounts[key] == 2 {
		// The group just became a duplicate: keep its text (whitespace
		// collapsed, original case) and one URL to show.
		a.titleInfo[key] = &titleAcc{title: collapseSpaces(p.Title), sample: p.URL}
	}
}

// finish turns every accumulator into the report's sorted, capped lists.
func (a *accumulator) finish(r *Report) {
	r.ByDepth = depthBuckets(a.depths)
	r.ByStatus = statusCounts(a.statuses)
	r.ByLanguage = sortedBuckets(a.languages, 0)
	r.BySection = sectionBuckets(a.sections)
	r.Redirects = sortedRedirects(a.redirects)
	r.Errors = sortedErrors(a.errors)
	r.DuplicateTitles = sortedTitles(a.titleCounts, a.titleInfo)
	r.Slowest = a.slowest.result()
	r.Largest = a.largest.result()
	r.Samples = a.sampler.result()
}

func bucketFor[K comparable](m map[K]*bucketAcc, key K) *bucketAcc {
	b, ok := m[key]
	if !ok {
		b = &bucketAcc{}
		m[key] = b
	}
	return b
}

// statusKey is the ByStatus key of a page: its exact code, or the
// pseudo-codes BLOQ/ERR when no response was obtained.
func statusKey(p store.Page) string {
	switch {
	case p.Status > 0:
		return strconv.Itoa(p.Status)
	case p.Blocked:
		return "BLOQ"
	default:
		return "ERR"
	}
}

// pageRef builds the compact reference shown in top-N lists and samples.
func pageRef(p store.Page) PageRef {
	ref := PageRef{URL: p.URL, Status: p.Status, DurationMs: p.DurationMs, Size: p.Size, Depth: p.Depth}
	switch {
	case p.Status >= 300 && p.Status <= 399 && p.RedirectTo != "":
		ref.Extra = p.RedirectTo
	case p.Status == 0 && !p.Blocked:
		ref.Extra = p.Error
	case p.NoIndex:
		ref.Extra = p.MetaRobots
	}
	return ref
}

// sampleKeys lists the Samples buckets a page belongs to; a page can be in
// several (a noindex 3xx, say).
func sampleKeys(p store.Page) []string {
	var keys []string
	switch {
	case p.Status >= 300 && p.Status <= 399:
		keys = append(keys, "3xx")
	case p.Status >= 400 && p.Status <= 499:
		keys = append(keys, "4xx")
	case p.Status >= 500 && p.Status <= 599:
		keys = append(keys, "5xx")
	case p.Status == 0 && !p.Blocked:
		keys = append(keys, "error")
	}
	if p.Blocked {
		keys = append(keys, "blocked")
	}
	if p.NoIndex {
		keys = append(keys, "noindex")
	}
	if p.ViaNoFollow {
		keys = append(keys, "via_nofollow")
	}
	return keys
}

// --- language and section ---

// languageAndSection derives the language and section buckets of a URL: the
// language is the first path segment when it is exactly two ASCII letters,
// the section is the segment right after it (or the first one when there is
// no language). Segments are percent-decoded so that /w%C3%B6hner and
// /wöhner are the same section.
func languageAndSection(rawURL string) (language, section string) {
	segments := pathSegments(rawURL)
	if len(segments) == 0 {
		return noLanguage, rootSection
	}

	language = noLanguage
	rest := segments
	if isLanguageCode(segments[0]) {
		language = strings.ToLower(segments[0])
		rest = segments[1:]
	}
	if len(rest) == 0 {
		return language, rootSection
	}
	return language, rest[0]
}

// pathSegments splits a URL's path into non-empty, percent-decoded
// segments. A segment that is not valid percent-encoding is kept as is.
func pathSegments(rawURL string) []string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	var out []string
	for _, seg := range strings.Split(u.EscapedPath(), "/") {
		if seg == "" {
			continue
		}
		decoded, err := url.PathUnescape(seg)
		if err != nil {
			decoded = seg
		}
		out = append(out, decoded)
	}
	return out
}

func isLanguageCode(seg string) bool {
	if len(seg) != 2 {
		return false
	}
	for i := 0; i < 2; i++ {
		c := seg[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// --- errors ---

// errorKindFor classifies a failed fetch, in the fixed order of
// specs/008-informe.md (the first match wins). Anything unrecognized
// becomes "otro: " plus the first 60 characters of the message with the
// page's own URL stripped out, so that near-identical errors on different
// URLs still group together.
func errorKindFor(errMsg, pageURL string) string {
	switch {
	case strings.HasPrefix(errMsg, "robots.txt:"):
		return "robots.txt"
	case strings.Contains(errMsg, "Client.Timeout"), strings.Contains(errMsg, "deadline exceeded"):
		return "timeout"
	case strings.Contains(errMsg, "connection refused"):
		return "conexión rechazada"
	case strings.Contains(errMsg, "tls:"), strings.Contains(errMsg, "x509"):
		return "tls"
	case strings.Contains(errMsg, "EOF"), strings.Contains(errMsg, "reset by peer"):
		return "conexión cerrada"
	case strings.Contains(errMsg, "no such host"):
		return "dns"
	case strings.Contains(errMsg, "truncated"), strings.Contains(errMsg, "body"):
		return "cuerpo"
	}

	rest := errMsg
	if pageURL != "" {
		rest = strings.ReplaceAll(rest, pageURL, "")
	}
	rest = collapseSpaces(rest)
	if runes := []rune(rest); len(runes) > 60 {
		rest = string(runes[:60])
	}
	return "otro: " + rest
}

// --- redirects ---

// redirectPattern classifies a redirect by comparing origin and target,
// following the table in specs/008-informe.md. Only path and query are
// compared (the fragment never reaches the server), and a redirect that
// leaves the host (ignoring a leading "www.") is simply "otro host".
func redirectPattern(from, to string) string {
	fromURL, err1 := url.Parse(from)
	toURL, err2 := url.Parse(to)
	if err1 != nil || err2 != nil {
		return "otro host"
	}
	if bareHost(fromURL) != bareHost(toURL) {
		return "otro host"
	}

	fromPQ := pathQuery(fromURL)
	toPQ := pathQuery(toURL)

	switch {
	case toPQ == fromPQ+"/":
		return "añade barra final"
	case toPQ+"/" == fromPQ:
		return "quita barra final"
	case fromURL.Scheme == "http" && toURL.Scheme == "https" && fromPQ == toPQ:
		return "http→https"
	case fromPQ != toPQ && strings.EqualFold(fromPQ, toPQ):
		return "cambia mayúsculas"
	case fromURL.RawQuery != "" && toURL.RawQuery == "" && toURL.EscapedPath() == fromURL.EscapedPath():
		return "quita query"
	}

	if fromURL.RawQuery == toURL.RawQuery {
		fromSegs := pathSegments(from)
		toSegs := pathSegments(to)
		if p, ok := segmentPattern(fromSegs, toSegs); ok {
			return p
		}
		if p, ok := prefixPattern(fromSegs, toSegs); ok {
			return p
		}
	}
	return "otra ruta"
}

// segmentPattern reports the "segmento N: a → b" pattern when both paths
// have the same number of segments and exactly one of them differs.
func segmentPattern(fromSegs, toSegs []string) (string, bool) {
	if len(fromSegs) != len(toSegs) {
		return "", false
	}
	diff := -1
	for i := range fromSegs {
		if fromSegs[i] != toSegs[i] {
			if diff != -1 {
				return "", false
			}
			diff = i
		}
	}
	if diff == -1 {
		return "", false
	}
	return fmt.Sprintf("segmento %d: %s → %s", diff+1, fromSegs[diff], toSegs[diff]), true
}

// prefixPattern reports the "añade prefijo /x" pattern when the target is
// the origin with one extra segment inserted at the front (/ → /en).
func prefixPattern(fromSegs, toSegs []string) (string, bool) {
	if len(toSegs) != len(fromSegs)+1 {
		return "", false
	}
	for i := range fromSegs {
		if fromSegs[i] != toSegs[i+1] {
			return "", false
		}
	}
	return "añade prefijo /" + toSegs[0], true
}

// bareHost is a URL's host, lowercased and without a leading "www.".
func bareHost(u *url.URL) string {
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// pathQuery is the part of a URL a redirect can meaningfully change: path
// plus query, never the fragment.
func pathQuery(u *url.URL) string {
	if u.RawQuery == "" {
		return u.EscapedPath()
	}
	return u.EscapedPath() + "?" + u.RawQuery
}

// --- top-N lists ---

// topPages keeps the best n PageRefs seen so far, in order, without ever
// holding more than n of them: the pass visits millions of pages but only
// the top ten are wanted.
type topPages struct {
	n      int
	better func(a, b PageRef) bool
	items  []PageRef
}

func (t *topPages) offer(ref PageRef) {
	if len(t.items) == t.n && !t.better(ref, t.items[len(t.items)-1]) {
		return
	}
	i := sort.Search(len(t.items), func(i int) bool { return t.better(ref, t.items[i]) })
	t.items = append(t.items, PageRef{})
	copy(t.items[i+1:], t.items[i:])
	t.items[i] = ref
	if len(t.items) > t.n {
		t.items = t.items[:t.n]
	}
}

func (t *topPages) result() []PageRef {
	if t.items == nil {
		return []PageRef{}
	}
	return t.items
}

func slowerFirst(a, b PageRef) bool {
	if a.DurationMs != b.DurationMs {
		return a.DurationMs > b.DurationMs
	}
	return a.URL < b.URL
}

func biggerFirst(a, b PageRef) bool {
	if a.Size != b.Size {
		return a.Size > b.Size
	}
	return a.URL < b.URL
}

// --- reservoir sampling ---

// sampler keeps up to samplesPerKey uniformly random pages per key, in a
// single pass and without holding the candidates: classic reservoir
// sampling, seeded from the package's seed variable so a test can pin it.
type sampler struct {
	rng  *rand.Rand
	seen map[string]int
	out  map[string][]PageRef
}

func newSampler(seed int64) *sampler {
	return &sampler{
		rng:  rand.New(rand.NewSource(seed)),
		seen: map[string]int{},
		out:  map[string][]PageRef{},
	}
}

func (s *sampler) offer(key string, ref PageRef) {
	s.seen[key]++
	if len(s.out[key]) < samplesPerKey {
		s.out[key] = append(s.out[key], ref)
		return
	}
	if j := s.rng.Intn(s.seen[key]); j < samplesPerKey {
		s.out[key][j] = ref
	}
}

func (s *sampler) result() map[string][]PageRef {
	return s.out
}

// --- sorting and capping ---

func depthBuckets(m map[int]*bucketAcc) []Bucket {
	depths := make([]int, 0, len(m))
	for d := range m {
		depths = append(depths, d)
	}
	sort.Ints(depths)

	out := make([]Bucket, 0, len(depths))
	for _, d := range depths {
		out = append(out, m[d].bucket(strconv.Itoa(d)))
	}
	return out
}

// sortedBuckets orders buckets by total descending (ties by key, so the
// output never depends on map iteration order) and keeps at most limit of
// them; limit <= 0 keeps them all.
func sortedBuckets(m map[string]*bucketAcc, limit int) []Bucket {
	keys := sortedKeysByTotal(m)
	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
	}
	out := make([]Bucket, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k].bucket(k))
	}
	return out
}

func sortedKeysByTotal(m map[string]*bucketAcc) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]].Total != m[keys[j]].Total {
			return m[keys[i]].Total > m[keys[j]].Total
		}
		return keys[i] < keys[j]
	})
	return keys
}

// sectionBuckets lists the maxSections largest sections and, when there are
// more, folds every remaining section into a single "(otras N secciones)"
// row, so the totals of the table still add up to the whole crawl.
func sectionBuckets(m map[string]*bucketAcc) []Bucket {
	keys := sortedKeysByTotal(m)
	if len(keys) <= maxSections {
		return sortedBuckets(m, 0)
	}

	out := make([]Bucket, 0, maxSections+1)
	for _, k := range keys[:maxSections] {
		out = append(out, m[k].bucket(k))
	}
	rest := &bucketAcc{}
	for _, k := range keys[maxSections:] {
		m[k].mergeInto(rest)
	}
	return append(out, rest.bucket(fmt.Sprintf("(otras %d secciones)", len(keys)-maxSections)))
}

func statusCounts(m map[string]int) []StatusCount {
	out := make([]StatusCount, 0, len(m))
	for k, n := range m {
		out = append(out, StatusCount{Status: k, N: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Status < out[j].Status
	})
	return out
}

func sortedRedirects(m map[string]*RedirectPattern) []RedirectPattern {
	out := make([]RedirectPattern, 0, len(m))
	for _, rp := range m {
		out = append(out, *rp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Pattern < out[j].Pattern
	})
	if len(out) > maxRedirectPatterns {
		out = out[:maxRedirectPatterns]
	}
	return out
}

func sortedErrors(m map[string]*ErrorKind) []ErrorKind {
	out := make([]ErrorKind, 0, len(m))
	for _, ek := range m {
		out = append(out, *ek)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Kind < out[j].Kind
	})
	if len(out) > maxErrorKinds {
		out = out[:maxErrorKinds]
	}
	return out
}

func sortedTitles(counts map[uint64]int, info map[uint64]*titleAcc) []TitleGroup {
	out := make([]TitleGroup, 0, len(info))
	for key, acc := range info {
		out = append(out, TitleGroup{Title: acc.title, N: counts[key], Sample: acc.sample})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Title < out[j].Title
	})
	if len(out) > maxTitleGroups {
		out = out[:maxTitleGroups]
	}
	return out
}

// rankBroken ranks the scanned broken pages by referrer count (ties by
// URL) and keeps the maxBroken worst.
func rankBroken(broken []store.BrokenPage) []BrokenRanked {
	out := make([]BrokenRanked, 0, len(broken))
	for _, b := range broken {
		out = append(out, BrokenRanked{
			URL:       b.Page.URL,
			Status:    b.Page.Status,
			Error:     b.Page.Error,
			Referrers: b.ReferrersCount,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Referrers != out[j].Referrers {
			return out[i].Referrers > out[j].Referrers
		}
		return out[i].URL < out[j].URL
	})
	if len(out) > maxBroken {
		out = out[:maxBroken]
	}
	return out
}

// --- text helpers ---

// normalizeTitle is the key duplicate titles are grouped by: lowercased,
// with runs of whitespace collapsed to a single space.
func normalizeTitle(title string) string {
	return strings.ToLower(collapseSpaces(title))
}

// collapseSpaces trims the string and collapses every run of whitespace
// into a single space.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
