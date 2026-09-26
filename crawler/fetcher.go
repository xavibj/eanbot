package crawler

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Response is the result of fetching a single URL.
type Response struct {
	Status      int
	ContentType string // raw header value
	Location    string // Location header, if the status is 3xx
	XRobotsTag  string // X-Robots-Tag header(s), joined by ", " if there are several; "" if none
	Body        []byte // at most MaxBodyBytes
	Truncated   bool   // true if the body was cut short at MaxBodyBytes
	Size        int64  // bytes read (== len(Body))
	Duration    time.Duration
}

// Fetcher performs the single piece of I/O the crawler package needs:
// fetching one URL. It is injected into Run so that tests never touch the
// network.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (*Response, error)
}

// HTTPFetcher implements Fetcher using net/http.
type HTTPFetcher struct {
	client    *http.Client
	userAgent string
	maxBody   int64

	// Headers holds extra headers added to every request (pages, robots.txt
	// and sitemaps alike) after User-Agent and Accept, so a header here can
	// override either of them — including User-Agent itself. Header names
	// are canonicalized (http.CanonicalHeaderKey) when sent.
	Headers map[string]string

	// Origin forces the origin server address ("ip" or "ip:port", see
	// crawler.SplitOrigin) for requests whose host matches OriginHost,
	// bypassing DNS resolution — the equivalent of `curl --resolve`, used
	// to skip a CDN/WAF such as Cloudflare. The Host header and TLS SNI are
	// left untouched (still the request's own host), only the dialed
	// address changes. "" (the default) disables the override entirely.
	Origin string
	// OriginHost is the host requests are matched against before applying
	// Origin: a request to OriginHost or its "www." variant (compared with
	// strings.EqualFold on both sides after stripping any "www." prefix)
	// is redirected to Origin; any other host dials normally, even after a
	// scope change (e.g. the seed redirecting to another domain).
	OriginHost string
	// InsecureTLS disables TLS certificate verification. Unlike Origin, it
	// applies to every request regardless of host.
	InsecureTLS bool

	// transportOnce builds the custom http.Transport (DialContext override
	// for Origin, TLSClientConfig.InsecureSkipVerify for InsecureTLS) the
	// first time Fetch runs, so that Origin/OriginHost/InsecureTLS can
	// still be set after NewHTTPFetcher returns. sync.Once makes this safe
	// under the concurrent Fetch calls crawler.Run issues.
	transportOnce sync.Once
}

// NewHTTPFetcher builds an HTTPFetcher. It never follows redirects (3xx
// responses are returned as-is, with their Location header exposed on
// Response.Location) and reads at most maxBody+1 bytes from the response
// body, so that Truncated can be computed without unbounded reads.
func NewHTTPFetcher(userAgent string, timeout time.Duration, maxBody int64) *HTTPFetcher {
	return &HTTPFetcher{
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		userAgent: userAgent,
		maxBody:   maxBody,
	}
}

// ensureTransport builds h.client's transport on first use, cloning
// http.DefaultTransport and wiring in the Origin/InsecureTLS overrides. It
// is called from Fetch, guarded by transportOnce, so Origin, OriginHost and
// InsecureTLS may be set any time before the first Fetch call.
func (h *HTTPFetcher) ensureTransport() {
	h.transportOnce.Do(func() {
		t := http.DefaultTransport.(*http.Transport).Clone()

		if t.TLSClientConfig == nil {
			t.TLSClientConfig = &tls.Config{}
		}
		t.TLSClientConfig.InsecureSkipVerify = h.InsecureTLS

		baseDial := t.DialContext
		origin, originHost := h.Origin, h.OriginHost
		t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if origin != "" {
				if reqHost, reqPort, err := net.SplitHostPort(addr); err == nil && hostMatchesOrigin(reqHost, originHost) {
					if originHost2, originPort, err := SplitOrigin(origin); err == nil {
						if originPort == "" {
							originPort = reqPort
						}
						addr = net.JoinHostPort(originHost2, originPort)
					}
				}
			}
			return baseDial(ctx, network, addr)
		}

		h.client.Transport = t
	})
}

// hostMatchesOrigin reports whether reqHost is the host requests should be
// redirected to Origin for: reqHost equals originHost, ignoring case and any
// "www." prefix on either side.
func hostMatchesOrigin(reqHost, originHost string) bool {
	if originHost == "" {
		return false
	}
	return strings.EqualFold(trimWWW(reqHost), trimWWW(originHost))
}

func trimWWW(host string) string {
	trimmed := strings.TrimPrefix(strings.ToLower(host), "www.")
	return trimmed
}

// Fetch performs a GET request against url.
func (h *HTTPFetcher) Fetch(ctx context.Context, url string) (*Response, error) {
	h.ensureTransport()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", h.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	for k, v := range h.Headers {
		req.Header.Set(http.CanonicalHeaderKey(k), v)
	}

	start := time.Now()
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, h.maxBody+1))
	duration := time.Since(start)
	if readErr != nil {
		return nil, readErr
	}

	truncated := false
	if int64(len(body)) > h.maxBody {
		body = body[:h.maxBody]
		truncated = true
	}

	return &Response{
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Location:    resp.Header.Get("Location"),
		XRobotsTag:  strings.Join(resp.Header.Values("X-Robots-Tag"), ", "),
		Body:        body,
		Truncated:   truncated,
		Size:        int64(len(body)),
		Duration:    duration,
	}, nil
}
