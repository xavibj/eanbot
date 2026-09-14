package crawler

import (
	"context"
	"fmt"
	"sync"
)

// fakeFetcher is an in-memory Fetcher used by Run tests, so that no test
// ever touches the network. Responses (or errors) are looked up by exact
// URL string.
type fakeFetcher struct {
	mu        sync.Mutex
	responses map[string]*Response
	errs      map[string]error
	calls     []string

	// onCall, if set, runs (under the fetcher's lock, before the response
	// is returned) for every call; it is used by cancellation tests to
	// cancel a context after a certain number of fetches.
	onCall func(url string, callIndex int)
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{
		responses: map[string]*Response{},
		errs:      map[string]error{},
	}
}

func (f *fakeFetcher) set(url string, resp *Response) {
	f.responses[url] = resp
}

func (f *fakeFetcher) setErr(url string, err error) {
	f.errs[url] = err
}

func (f *fakeFetcher) Fetch(ctx context.Context, url string) (*Response, error) {
	f.mu.Lock()
	idx := len(f.calls)
	f.calls = append(f.calls, url)
	hook := f.onCall
	f.mu.Unlock()

	if hook != nil {
		hook(url, idx)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.errs[url]; ok {
		return nil, err
	}
	if resp, ok := f.responses[url]; ok {
		return resp, nil
	}
	return nil, fmt.Errorf("fakeFetcher: no response configured for %s", url)
}

func (f *fakeFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeFetcher) calledURLs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// memorySink collects every Page delivered by Run, in delivery order.
type memorySink struct {
	mu    sync.Mutex
	pages []Page
}

func (s *memorySink) Page(p Page) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages = append(s.pages, p)
}

func (s *memorySink) all() []Page {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Page, len(s.pages))
	copy(out, s.pages)
	return out
}

func (s *memorySink) urls() []string {
	pages := s.all()
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = p.URL
	}
	return out
}

func htmlResponse(body string) *Response {
	return &Response{
		Status:      200,
		ContentType: "text/html; charset=utf-8",
		Body:        []byte(body),
		Size:        int64(len(body)),
	}
}

func textResponse(status int, contentType, body string) *Response {
	return &Response{
		Status:      status,
		ContentType: contentType,
		Body:        []byte(body),
		Size:        int64(len(body)),
	}
}

func redirectResponse(status int, location string) *Response {
	return &Response{Status: status, Location: location}
}

func robotsResponse(body string) *Response {
	return &Response{Status: 200, ContentType: "text/plain", Body: []byte(body), Size: int64(len(body))}
}
