// Package crawler implements the crawl domain logic for eanbot: URL
// normalization, scope checks, robots.txt parsing, HTML link extraction,
// sitemap discovery, the crawl frontier and the crawl engine itself. The
// only I/O performed by this package goes through the injectable Fetcher
// interface and the Sink interface; no SQLite, no cmd, no server.
package crawler

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Config holds all parameters for a single crawl run.
type Config struct {
	Seed              string        // seed URL (required, http/https)
	MaxPages          int           // >0; default 500
	MaxDepth          int           // >=0; default 10
	Concurrency       int           // >0; default 4
	Delay             time.Duration // >=0; default 500ms
	Timeout           time.Duration // per request; default 15s
	MaxBodyBytes      int64         // default 2 MiB
	UserAgent         string        // default "EANBot/0.1 (+https://xavi.net)"
	RobotsToken       string        // default "eanbot"
	IncludeSubdomains bool
	IgnoreRobots      bool
	UseSitemaps       bool              // default true (see Defaults)
	Headers           map[string]string // extra headers sent on ALL requests (pages, robots, sitemaps)
}

// Defaults returns the default configuration values, as fixed in
// specs/001-plan-tecnico.md.
func Defaults() Config {
	return Config{
		MaxPages:     500,
		MaxDepth:     10,
		Concurrency:  4,
		Delay:        500 * time.Millisecond,
		Timeout:      15 * time.Second,
		MaxBodyBytes: 2 * 1024 * 1024,
		UserAgent:    "EANBot/0.1 (+https://xavi.net)",
		RobotsToken:  "eanbot",
		UseSitemaps:  true,
	}
}

// Validate reports every violation found in c, with messages in Spanish, as
// fixed by specs/002-crawler.md. An empty slice means c is valid.
func (c Config) Validate() []string {
	var errs []string

	seed := strings.TrimSpace(c.Seed)
	if seed == "" {
		errs = append(errs, "la semilla es obligatoria")
	} else if !isAbsoluteHTTPURL(seed) {
		errs = append(errs, "la semilla debe ser una URL http o https absoluta")
	}

	if c.MaxPages <= 0 {
		errs = append(errs, "max_pages debe ser mayor que 0")
	}
	if c.MaxDepth < 0 {
		errs = append(errs, "max_depth no puede ser negativo")
	}
	if c.Concurrency <= 0 {
		errs = append(errs, "concurrency debe ser mayor que 0")
	}
	if c.Delay < 0 {
		errs = append(errs, "delay_ms no puede ser negativo")
	}

	if len(c.Headers) > 0 {
		names := make([]string, 0, len(c.Headers))
		for name := range c.Headers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if !isValidHeaderName(name) {
				errs = append(errs, fmt.Sprintf("cabecera no válida: %q", name))
				continue
			}
			if strings.ContainsAny(c.Headers[name], "\r\n") {
				errs = append(errs, fmt.Sprintf("valor de cabecera no válido: %q", name))
			}
		}
	}

	return errs
}

// isValidHeaderName reports whether name is a valid HTTP token: one or more
// letters, digits or the characters !#$%&'*+-.^_`|~.
func isValidHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !isTokenRune(r) {
			return false
		}
	}
	return true
}

func isTokenRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		return true
	default:
		return false
	}
}

func isAbsoluteHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// WithDefaults returns a copy of c with every zero-valued numeric/string
// field replaced by the corresponding value from Defaults(), except Delay:
// zero is a legitimate, explicit "no courtesy delay" setting (used by every
// test), not a sentinel for "unset", so it is left untouched — exactly like
// the boolean fields, which are never touched either.
func (c Config) WithDefaults() Config {
	d := Defaults()
	if c.MaxPages == 0 {
		c.MaxPages = d.MaxPages
	}
	if c.MaxDepth == 0 {
		c.MaxDepth = d.MaxDepth
	}
	if c.Concurrency == 0 {
		c.Concurrency = d.Concurrency
	}
	if c.Timeout == 0 {
		c.Timeout = d.Timeout
	}
	if c.MaxBodyBytes == 0 {
		c.MaxBodyBytes = d.MaxBodyBytes
	}
	if c.UserAgent == "" {
		c.UserAgent = d.UserAgent
	}
	if c.RobotsToken == "" {
		c.RobotsToken = d.RobotsToken
	}
	return c
}
