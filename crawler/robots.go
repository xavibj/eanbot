package crawler

import (
	"bufio"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Robots holds a parsed robots.txt file.
type Robots struct {
	groups      []robotsGroup
	sitemapURLs []string
	disallowAll bool
	reason      string // why everything is disallowed (robots.txt unavailable); "" for rule-based files
}

type robotsGroup struct {
	agents        []string // lower-cased user-agent values, "*" included as-is
	rules         []robotsRule
	crawlDelay    time.Duration
	hasCrawlDelay bool
}

type robotsRule struct {
	allow   bool
	pattern string
}

// ParseRobots parses a robots.txt document from r. It never returns nil.
func ParseRobots(r io.Reader) *Robots {
	rob := &Robots{}

	var current *robotsGroup
	ruleSeen := false

	flush := func() {
		if current != nil {
			rob.groups = append(rob.groups, *current)
		}
	}
	startGroup := func() {
		current = &robotsGroup{}
		ruleSeen = false
	}

	scanner := bufio.NewScanner(r)
	// robots.txt files can contain very long lines; use a generous buffer.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.IndexByte(line, '#'); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		field := strings.ToLower(strings.TrimSpace(line[:colon]))
		value := strings.TrimSpace(line[colon+1:])

		switch field {
		case "user-agent":
			if current != nil && ruleSeen {
				flush()
				startGroup()
			} else if current == nil {
				startGroup()
			}
			current.agents = append(current.agents, strings.ToLower(value))
		case "allow", "disallow":
			if current == nil {
				continue
			}
			if value == "" {
				// An empty Disallow means "allow everything"; an empty
				// Allow has no effect either. Skip both: a rule-less group
				// already defaults to allow.
				ruleSeen = true
				continue
			}
			current.rules = append(current.rules, robotsRule{allow: field == "allow", pattern: value})
			ruleSeen = true
		case "crawl-delay":
			if current == nil {
				continue
			}
			if secs, err := strconv.ParseFloat(value, 64); err == nil && secs >= 0 {
				current.crawlDelay = time.Duration(secs * float64(time.Second))
				current.hasCrawlDelay = true
			}
			ruleSeen = true
		case "sitemap":
			if value != "" {
				rob.sitemapURLs = append(rob.sitemapURLs, value)
			}
		}
	}
	flush()

	return rob
}

// AllowAll returns a Robots value that permits crawling everything.
func AllowAll() *Robots {
	return &Robots{}
}

// DisallowAll returns a Robots value that forbids crawling anything.
func DisallowAll() *Robots {
	return &Robots{disallowAll: true}
}

// DisallowAllBecause is DisallowAll with a human-readable reason, used when
// robots.txt could not be fetched (5xx, network or TLS error). The reason is
// surfaced on every blocked page so the operator can tell "blocked by a
// rule" from "robots.txt unavailable".
func DisallowAllBecause(reason string) *Robots {
	return &Robots{disallowAll: true, reason: reason}
}

// BlockReason returns the reason attached by DisallowAllBecause, or "".
func (r *Robots) BlockReason() string {
	if r == nil {
		return ""
	}
	return r.reason
}

// Allowed reports whether path (which includes the query string, if any) may
// be fetched by a crawler identifying itself with token.
func (r *Robots) Allowed(token, path string) bool {
	if r == nil {
		return true
	}
	if r.disallowAll {
		return false
	}
	g := r.selectGroup(token)
	if g == nil {
		return true
	}
	return matchRules(g.rules, path)
}

// CrawlDelay returns the Crawl-delay declared for token's group, or 0 if
// none was declared.
func (r *Robots) CrawlDelay(token string) time.Duration {
	if r == nil {
		return 0
	}
	g := r.selectGroup(token)
	if g == nil || !g.hasCrawlDelay {
		return 0
	}
	return g.crawlDelay
}

// Sitemaps returns every "Sitemap:" URL declared in the document.
func (r *Robots) Sitemaps() []string {
	if r == nil {
		return nil
	}
	return r.sitemapURLs
}

// selectGroup picks the group whose User-agent contains token
// (case-insensitive substring match); falling back to the "*" group, or nil
// if neither exists.
func (r *Robots) selectGroup(token string) *robotsGroup {
	token = strings.ToLower(token)
	var star *robotsGroup
	for i := range r.groups {
		g := &r.groups[i]
		for _, a := range g.agents {
			if a == "*" {
				if star == nil {
					star = g
				}
				continue
			}
			if token != "" && strings.Contains(a, token) {
				return g
			}
		}
	}
	return star
}

// matchRules applies the "longest pattern wins, Allow wins ties" algorithm
// to path, returning true when no rule matches (default allow).
func matchRules(rules []robotsRule, path string) bool {
	bestLen := -1
	bestAllow := true
	matched := false

	for _, rule := range rules {
		re := compileRobotsPattern(rule.pattern)
		if re == nil || !re.MatchString(path) {
			continue
		}
		length := len(rule.pattern)
		if length > bestLen || (length == bestLen && rule.allow) {
			bestLen = length
			bestAllow = rule.allow
			matched = true
		}
	}

	if !matched {
		return true
	}
	return bestAllow
}

// ParseRobotsDirectives interprets both the content of a <meta name="robots">
// tag and the X-Robots-Tag header: comma-separated directives,
// case-insensitive, recognizing noindex, nofollow and none (= both). A
// directive may carry an agent prefix (the X-Robots-Tag format, e.g.
// "googlebot: noindex"); it applies only when the prefix contains token
// (case-insensitive) or is "*". A directive without a prefix always applies.
func ParseRobotsDirectives(value, token string) (noindex, nofollow bool) {
	token = strings.ToLower(strings.TrimSpace(token))
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		directive := strings.ToLower(part)
		if idx := strings.IndexByte(part, ':'); idx >= 0 {
			prefix := strings.ToLower(strings.TrimSpace(part[:idx]))
			if prefix != "*" && !strings.Contains(prefix, token) {
				continue
			}
			directive = strings.ToLower(strings.TrimSpace(part[idx+1:]))
		}
		switch directive {
		case "noindex":
			noindex = true
		case "nofollow":
			nofollow = true
		case "none":
			noindex = true
			nofollow = true
		}
	}
	return noindex, nofollow
}

// compileRobotsPattern turns a robots.txt path pattern (where "*" matches
// any sequence of characters and a trailing "$" anchors the end of the
// match) into a regular expression anchored at the start of the string.
func compileRobotsPattern(pattern string) *regexp.Regexp {
	endAnchor := strings.HasSuffix(pattern, "$")
	p := pattern
	if endAnchor {
		p = strings.TrimSuffix(p, "$")
	}
	parts := strings.Split(p, "*")
	for i, part := range parts {
		parts[i] = regexp.QuoteMeta(part)
	}
	expr := "^" + strings.Join(parts, ".*")
	if endAnchor {
		expr += "$"
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil
	}
	return re
}
