package crawler

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/xml"
	"io"
	"net/url"
	"strings"
)

const maxSitemapURLs = 10000

type sitemapIndexXML struct {
	XMLName  xml.Name `xml:"sitemapindex"`
	Sitemaps []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
}

type urlsetXML struct {
	XMLName xml.Name `xml:"urlset"`
	URLs    []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

// collectSitemaps discovers URLs from every sitemap referenced by robots
// and pushes the in-scope ones into fr at depth 1. Sitemap errors are
// ignored: they never abort the crawl. stats.Sitemaps is set to the number
// of <loc> entries processed (bounded by maxSitemapURLs).
func collectSitemaps(ctx context.Context, cfg Config, f Fetcher, robots *Robots, base *url.URL, seedHost string, fr *Frontier, stats *Stats) {
	if !cfg.UseSitemaps || cfg.IgnoreRobots {
		return
	}

	count := 0

	var visit func(loc string, depth int)
	visit = func(loc string, depth int) {
		if depth > 2 || count >= maxSitemapURLs {
			return
		}
		norm, err := Normalize(loc, base)
		if err != nil {
			return
		}
		resp, err := f.Fetch(ctx, norm)
		if err != nil || resp == nil {
			return
		}

		body := resp.Body
		if looksGzip(body) || strings.HasSuffix(strings.ToLower(norm), ".gz") {
			decompressed, derr := gunzipAll(body)
			if derr != nil {
				return
			}
			body = decompressed
		}

		var idx sitemapIndexXML
		if err := xml.Unmarshal(body, &idx); err == nil && len(idx.Sitemaps) > 0 {
			for _, sm := range idx.Sitemaps {
				if count >= maxSitemapURLs {
					return
				}
				if sm.Loc == "" {
					continue
				}
				visit(sm.Loc, depth+1)
			}
			return
		}

		var us urlsetXML
		if err := xml.Unmarshal(body, &us); err != nil {
			return
		}
		for _, entry := range us.URLs {
			if count >= maxSitemapURLs {
				return
			}
			count++
			if entry.Loc == "" {
				continue
			}
			normURL, err := Normalize(entry.Loc, base)
			if err != nil {
				continue
			}
			pu, err := url.Parse(normURL)
			if err != nil {
				continue
			}
			if SameSite(seedHost, pu.Hostname(), cfg.IncludeSubdomains) {
				fr.Push(normURL, 1)
			}
		}
	}

	for _, sm := range robots.Sitemaps() {
		if count >= maxSitemapURLs {
			break
		}
		visit(sm, 1)
	}

	stats.Sitemaps = count
}

func looksGzip(b []byte) bool {
	return len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b
}

func gunzipAll(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}
