package crawler

import (
	"strings"
	"testing"
	"time"
)

func TestParseRobotsEmptyFile(t *testing.T) {
	r := ParseRobots(strings.NewReader(""))
	if !r.Allowed("eanbot", "/anything") {
		t.Error("empty robots.txt should allow everything")
	}
	if len(r.Sitemaps()) != 0 {
		t.Errorf("expected no sitemaps, got %v", r.Sitemaps())
	}
	if r.CrawlDelay("eanbot") != 0 {
		t.Errorf("expected zero crawl-delay, got %v", r.CrawlDelay("eanbot"))
	}
}

func TestParseRobotsGroupSelection(t *testing.T) {
	doc := `
User-agent: eanbot-crawler
Disallow: /private/

User-agent: *
Disallow: /
`
	r := ParseRobots(strings.NewReader(doc))

	if !r.Allowed("eanbot", "/public/page.html") {
		t.Error("specific group should allow /public/")
	}
	if r.Allowed("eanbot", "/private/secret.html") {
		t.Error("specific group should disallow /private/")
	}
	// A token with no matching group and no "*" group would default allow,
	// but here "*" disallows everything.
	if r.Allowed("othercrawler", "/public/page.html") {
		t.Error("fallback '*' group should disallow everything")
	}
}

func TestParseRobotsNoMatchingGroupAllowsAll(t *testing.T) {
	doc := `
User-agent: somebot
Disallow: /
`
	r := ParseRobots(strings.NewReader(doc))
	if !r.Allowed("eanbot", "/anything") {
		t.Error("with no matching group and no '*' group, everything should be allowed")
	}
}

func TestParseRobotsLongestMatchWins(t *testing.T) {
	doc := `
User-agent: *
Disallow: /a
Allow: /a/b
`
	r := ParseRobots(strings.NewReader(doc))
	if !r.Allowed("eanbot", "/a/b/c") {
		t.Error("longer Allow pattern should win over shorter Disallow")
	}
	if r.Allowed("eanbot", "/a/x") {
		t.Error("/a/x should still be disallowed")
	}
}

func TestParseRobotsAllowWinsTie(t *testing.T) {
	doc := `
User-agent: *
Disallow: /a/b
Allow: /a/b
`
	r := ParseRobots(strings.NewReader(doc))
	if !r.Allowed("eanbot", "/a/b") {
		t.Error("on a length tie, Allow should win")
	}
}

func TestParseRobotsWildcardAndEndAnchor(t *testing.T) {
	doc := `
User-agent: *
Disallow: /*.pdf$
Allow: /docs/*
`
	r := ParseRobots(strings.NewReader(doc))
	if r.Allowed("eanbot", "/report.pdf") {
		t.Error("/report.pdf should be disallowed by /*.pdf$")
	}
	if !r.Allowed("eanbot", "/report.pdf.html") {
		t.Error("/report.pdf.html should be allowed, $ anchors the end")
	}
	if !r.Allowed("eanbot", "/docs/anything") {
		t.Error("/docs/anything should be allowed by /docs/*")
	}
}

func TestParseRobotsCrawlDelay(t *testing.T) {
	doc := `
User-agent: eanbot
Crawl-delay: 2.5

User-agent: *
Crawl-delay: 1
`
	r := ParseRobots(strings.NewReader(doc))
	if got, want := r.CrawlDelay("eanbot"), 2500*time.Millisecond; got != want {
		t.Errorf("CrawlDelay(eanbot) = %v, want %v", got, want)
	}
	if got, want := r.CrawlDelay("somethingelse"), 1*time.Second; got != want {
		t.Errorf("CrawlDelay(somethingelse) = %v, want %v", got, want)
	}
}

func TestParseRobotsSitemap(t *testing.T) {
	doc := `
Sitemap: https://example.com/sitemap1.xml
User-agent: *
Disallow:
Sitemap: https://example.com/sitemap2.xml
`
	r := ParseRobots(strings.NewReader(doc))
	want := []string{"https://example.com/sitemap1.xml", "https://example.com/sitemap2.xml"}
	got := r.Sitemaps()
	if len(got) != len(want) {
		t.Fatalf("Sitemaps() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Sitemaps()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if !r.Allowed("eanbot", "/anything") {
		t.Error("empty Disallow should allow everything")
	}
}

func TestParseRobotsConsecutiveUserAgentsShareRules(t *testing.T) {
	doc := `
User-agent: bot1
User-agent: bot2
Disallow: /shared/
`
	r := ParseRobots(strings.NewReader(doc))
	if r.Allowed("bot1", "/shared/x") {
		t.Error("bot1 should be disallowed from /shared/")
	}
	if r.Allowed("bot2", "/shared/x") {
		t.Error("bot2 should be disallowed from /shared/")
	}
}

func TestParseRobotsDirectives(t *testing.T) {
	tests := []struct {
		name         string
		value        string
		token        string
		wantNoIndex  bool
		wantNoFollow bool
	}{
		{name: "empty", value: "", token: "eanbot"},
		{name: "noindex only", value: "noindex", token: "eanbot", wantNoIndex: true},
		{name: "nofollow only", value: "nofollow", token: "eanbot", wantNoFollow: true},
		{name: "none means both", value: "none", token: "eanbot", wantNoIndex: true, wantNoFollow: true},
		{name: "uppercase and spacing", value: "NOINDEX, NOFOLLOW", token: "eanbot", wantNoIndex: true, wantNoFollow: true},
		{name: "matching agent prefix", value: "eanbot: noindex", token: "eanbot", wantNoIndex: true},
		{name: "matching agent prefix substring", value: "my-eanbot-crawler: nofollow", token: "eanbot", wantNoFollow: true},
		{name: "non-matching agent prefix", value: "googlebot: noindex", token: "eanbot"},
		{name: "wildcard agent prefix", value: "*: none", token: "eanbot", wantNoIndex: true, wantNoFollow: true},
		{name: "mixed prefixed and unprefixed", value: "googlebot: noindex, nofollow", token: "eanbot", wantNoFollow: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			noindex, nofollow := ParseRobotsDirectives(tt.value, tt.token)
			if noindex != tt.wantNoIndex || nofollow != tt.wantNoFollow {
				t.Errorf("ParseRobotsDirectives(%q, %q) = (%v, %v), want (%v, %v)",
					tt.value, tt.token, noindex, nofollow, tt.wantNoIndex, tt.wantNoFollow)
			}
		})
	}
}

func TestAllowAllAndDisallowAll(t *testing.T) {
	if !AllowAll().Allowed("eanbot", "/anything") {
		t.Error("AllowAll should allow everything")
	}
	if DisallowAll().Allowed("eanbot", "/anything") {
		t.Error("DisallowAll should disallow everything")
	}
}
