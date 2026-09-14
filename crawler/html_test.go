package crawler

import (
	"net/url"
	"testing"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("bad URL %q: %v", raw, err)
	}
	return u
}

func TestParseHTMLBaseHref(t *testing.T) {
	body := []byte(`<html><head><base href="https://other.example.com/dir/"></head>
<body><a href="page.html">link</a></body></html>`)
	page, err := ParseHTML(body, mustParseURL(t, "https://example.com/x/y"), "example.com", false)
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if len(page.Links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(page.Links))
	}
	if want := "https://other.example.com/dir/page.html"; page.Links[0].URL != want {
		t.Errorf("link URL = %q, want %q", page.Links[0].URL, want)
	}
}

func TestParseHTMLCanonical(t *testing.T) {
	body := []byte(`<html><head><link rel="canonical" href="https://example.com/canon"></head><body></body></html>`)
	page, err := ParseHTML(body, mustParseURL(t, "https://example.com/x"), "example.com", false)
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if page.Canonical != "https://example.com/canon" {
		t.Errorf("Canonical = %q, want https://example.com/canon", page.Canonical)
	}
}

func TestParseHTMLMetaRobots(t *testing.T) {
	body := []byte(`<html><head><meta name="robots" content="noindex, nofollow"></head><body></body></html>`)
	page, err := ParseHTML(body, mustParseURL(t, "https://example.com/x"), "example.com", false)
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if page.MetaRobots != "noindex, nofollow" {
		t.Errorf("MetaRobots = %q", page.MetaRobots)
	}
	if !page.NoIndex {
		t.Error("expected NoIndex true")
	}
	if !page.NoFollow {
		t.Error("expected NoFollow true")
	}
}

func TestParseHTMLAnchorNoFollow(t *testing.T) {
	body := []byte(`<html><body><a href="/a" rel="nofollow">a</a><a href="/b">b</a></body></html>`)
	page, err := ParseHTML(body, mustParseURL(t, "https://example.com/x"), "example.com", false)
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if len(page.Links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(page.Links))
	}
	byURL := map[string]Link{}
	for _, l := range page.Links {
		byURL[l.URL] = l
	}
	if !byURL["https://example.com/a"].NoFollow {
		t.Error("/a should be NoFollow")
	}
	if byURL["https://example.com/b"].NoFollow {
		t.Error("/b should not be NoFollow")
	}
}

func TestParseHTMLDiscardedSchemes(t *testing.T) {
	body := []byte(`<html><body>
<a href="mailto:x@example.com">mail</a>
<a href="tel:+1234">tel</a>
<a href="javascript:void(0)">js</a>
<a href="data:text/plain,hi">data</a>
<a href="#frag">frag only</a>
<a href="">empty</a>
<a href="/keep">keep</a>
</body></html>`)
	page, err := ParseHTML(body, mustParseURL(t, "https://example.com/x"), "example.com", false)
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if len(page.Links) != 1 {
		t.Fatalf("expected 1 link, got %d: %+v", len(page.Links), page.Links)
	}
	if page.Links[0].URL != "https://example.com/keep" {
		t.Errorf("unexpected link URL %q", page.Links[0].URL)
	}
}

func TestParseHTMLDedupeKeepsFirstText(t *testing.T) {
	body := []byte(`<html><body>
<a href="/same">first</a>
<a href="/same">second</a>
</body></html>`)
	page, err := ParseHTML(body, mustParseURL(t, "https://example.com/x"), "example.com", false)
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if len(page.Links) != 1 {
		t.Fatalf("expected 1 link after dedupe, got %d", len(page.Links))
	}
	if page.Links[0].Text != "first" {
		t.Errorf("Text = %q, want %q", page.Links[0].Text, "first")
	}
}

func TestParseHTMLAnchorTextCollapsedAndTruncated(t *testing.T) {
	body := []byte(`<html><body><a href="/a">  hello   <b>world</b>  </a></body></html>`)
	page, err := ParseHTML(body, mustParseURL(t, "https://example.com/x"), "example.com", false)
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if len(page.Links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(page.Links))
	}
	if page.Links[0].Text != "hello world" {
		t.Errorf("Text = %q, want %q", page.Links[0].Text, "hello world")
	}
}

func TestParseHTMLNoTitle(t *testing.T) {
	body := []byte(`<html><body><p>no title here</p></body></html>`)
	page, err := ParseHTML(body, mustParseURL(t, "https://example.com/x"), "example.com", false)
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if page.Title != "" {
		t.Errorf("Title = %q, want empty", page.Title)
	}
}

func TestParseHTMLTitleAndH1(t *testing.T) {
	body := []byte(`<html><head><title>  My   Title  </title></head><body><h1>Heading  One</h1></body></html>`)
	page, err := ParseHTML(body, mustParseURL(t, "https://example.com/x"), "example.com", false)
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	if page.Title != "My Title" {
		t.Errorf("Title = %q, want %q", page.Title, "My Title")
	}
	if page.H1 != "Heading One" {
		t.Errorf("H1 = %q, want %q", page.H1, "Heading One")
	}
}

func TestParseHTMLLinkInScope(t *testing.T) {
	body := []byte(`<html><body>
<a href="/same-site">same</a>
<a href="https://other.com/x">other</a>
</body></html>`)
	page, err := ParseHTML(body, mustParseURL(t, "https://example.com/x"), "example.com", false)
	if err != nil {
		t.Fatalf("ParseHTML error: %v", err)
	}
	byURL := map[string]Link{}
	for _, l := range page.Links {
		byURL[l.URL] = l
	}
	if !byURL["https://example.com/same-site"].InScope {
		t.Error("same-site link should be InScope")
	}
	if byURL["https://other.com/x"].InScope {
		t.Error("other.com link should not be InScope")
	}
}
