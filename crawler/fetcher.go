package crawler

import (
	"context"
	"io"
	"net/http"
	"time"
)

// Response is the result of fetching a single URL.
type Response struct {
	Status      int
	ContentType string // raw header value
	Location    string // Location header, if the status is 3xx
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

// Fetch performs a GET request against url.
func (h *HTTPFetcher) Fetch(ctx context.Context, url string) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", h.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")

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
		Body:        body,
		Truncated:   truncated,
		Size:        int64(len(body)),
		Duration:    duration,
	}, nil
}
