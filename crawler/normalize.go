package crawler

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Normalize resolves raw against base (if raw is not already absolute) and
// returns a canonical string form of the resulting URL:
//
//  1. raw is trimmed; an empty result is an error.
//  2. Relative references are resolved against base.
//  3. The scheme is lower-cased; only http/https are accepted.
//  4. The host is lower-cased; default ports (:80 for http, :443 for https)
//     are stripped.
//  5. The fragment is removed. An empty query ("?") is removed; any other
//     query string is kept as-is (not reordered).
//  6. An empty path becomes "/". Dot segments ("." and "..") are resolved.
//  7. URLs carrying userinfo are rejected.
func Normalize(raw string, base *url.URL) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("crawler: empty URL")
	}

	ref, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}

	var resolveBase *url.URL
	switch {
	case base != nil:
		resolveBase = base
	case ref.IsAbs():
		// Resolving ref against itself has no effect on scheme/host/query,
		// but it does run the reference through the same dot-segment
		// removal logic (RFC 3986 §5.3) as the base-provided case.
		resolveBase = ref
	default:
		return "", errors.New("crawler: relative URL without a base")
	}

	u := resolveBase.ResolveReference(ref)

	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("crawler: unsupported scheme %q", u.Scheme)
	}

	if u.User != nil {
		return "", errors.New("crawler: URL must not contain userinfo")
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("crawler: URL is missing a host")
	}
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		u.Host = host + ":" + port
	} else {
		u.Host = host
	}

	u.Fragment = ""
	u.RawFragment = ""
	u.ForceQuery = false

	if u.Path == "" {
		u.Path = "/"
	}

	return u.String(), nil
}

// SameSite reports whether host belongs to the same site as seedHost. Both
// hosts are compared after stripping a leading "www." prefix; when
// includeSubdomains is true, any host that is a subdomain of seedHost also
// counts as the same site.
func SameSite(seedHost, host string, includeSubdomains bool) bool {
	s := strings.TrimPrefix(strings.ToLower(seedHost), "www.")
	h := strings.TrimPrefix(strings.ToLower(host), "www.")
	if s == h {
		return true
	}
	if includeSubdomains && s != "" && strings.HasSuffix(h, "."+s) {
		return true
	}
	return false
}
