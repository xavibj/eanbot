package crawler

import (
	"bytes"
	"net/url"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// Link is an outbound link found on a crawled page.
type Link struct {
	URL      string // absolute, normalized
	Text     string // anchor text, collapsed whitespace, max 200 runes
	NoFollow bool
	InScope  bool
}

// ParseHTML parses the HTML document in body (whose canonical location is
// pageURL) and extracts the fields understood from the page: title,
// description, canonical link, meta robots directives, first H1 and every
// outbound link.
func ParseHTML(body []byte, pageURL *url.URL, seedHost string, includeSub bool) (Page, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return Page{}, err
	}

	base := pageURL
	if href := findBaseHref(doc); href != "" {
		if ref, err := url.Parse(href); err == nil {
			base = pageURL.ResolveReference(ref)
		}
	}

	var page Page
	titleSet, h1Set, descSet, robotsSet, canonicalSet := false, false, false, false, false
	seen := map[string]bool{}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				if !titleSet {
					page.Title = collapseSpaces(textContent(n))
					titleSet = true
				}
			case "h1":
				if !h1Set {
					page.H1 = collapseSpaces(textContent(n))
					h1Set = true
				}
			case "meta":
				name := strings.ToLower(strings.TrimSpace(attrOf(n, "name")))
				content := attrOf(n, "content")
				switch name {
				case "description":
					if !descSet {
						page.Description = strings.TrimSpace(content)
						descSet = true
					}
				case "robots":
					if !robotsSet {
						page.MetaRobots = content
						robotsSet = true
						for _, tok := range splitRobotsTokens(content) {
							switch tok {
							case "noindex":
								page.NoIndex = true
							case "nofollow":
								page.NoFollow = true
							}
						}
					}
				}
			case "link":
				if !canonicalSet && hasToken(attrOf(n, "rel"), "canonical") {
					href := strings.TrimSpace(attrOf(n, "href"))
					if href != "" {
						if norm, err := Normalize(href, base); err == nil {
							page.Canonical = norm
							canonicalSet = true
						}
					}
				}
			case "a":
				href, ok := attrOK(n, "href")
				if ok {
					href = strings.TrimSpace(href)
					if isUsableHref(href) {
						if norm, err := Normalize(href, base); err == nil && !seen[norm] {
							seen[norm] = true
							rel := attrOf(n, "rel")
							text := truncateRunes(collapseSpaces(textContent(n)), 200)
							link := Link{
								URL:      norm,
								Text:     text,
								NoFollow: hasToken(rel, "nofollow"),
							}
							if lu, err := url.Parse(norm); err == nil {
								link.InScope = SameSite(seedHost, lu.Hostname(), includeSub)
							}
							page.Links = append(page.Links, link)
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	return page, nil
}

func findBaseHref(doc *html.Node) string {
	var href string
	var walk func(*html.Node) bool
	walk = func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "base" {
			if v, ok := attrOK(n, "href"); ok && strings.TrimSpace(v) != "" {
				href = strings.TrimSpace(v)
				return true
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if walk(c) {
				return true
			}
		}
		return false
	}
	walk(doc)
	return href
}

func attrOf(n *html.Node, key string) string {
	v, _ := attrOK(n, key)
	return v
}

func attrOK(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val, true
		}
	}
	return "", false
}

func textContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// isUsableHref reports whether href should be considered as a candidate
// outbound link: non-empty, not a bare fragment, and not one of the
// mailto/tel/javascript/data schemes.
func isUsableHref(href string) bool {
	if href == "" || strings.HasPrefix(href, "#") {
		return false
	}
	lower := strings.ToLower(href)
	for _, scheme := range [...]string{"mailto:", "tel:", "javascript:", "data:"} {
		if strings.HasPrefix(lower, scheme) {
			return false
		}
	}
	return true
}

// hasToken reports whether s (a whitespace-separated token list, such as an
// HTML "rel" attribute) contains token, case-insensitively.
func hasToken(s, token string) bool {
	for _, f := range strings.Fields(s) {
		if strings.EqualFold(f, token) {
			return true
		}
	}
	return false
}

// splitRobotsTokens splits a <meta name="robots"> content value into
// lower-cased tokens, separated by commas and/or whitespace.
func splitRobotsTokens(content string) []string {
	fields := strings.FieldsFunc(content, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	for i, f := range fields {
		fields[i] = strings.ToLower(strings.TrimSpace(f))
	}
	return fields
}
