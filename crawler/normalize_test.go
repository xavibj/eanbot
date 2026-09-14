package crawler

import (
	"net/url"
	"testing"
)

func TestNormalize(t *testing.T) {
	base, err := url.Parse("https://example.com/dir/page.html")
	if err != nil {
		t.Fatalf("bad base: %v", err)
	}

	tests := []struct {
		name    string
		raw     string
		base    *url.URL
		want    string
		wantErr bool
	}{
		{
			name: "relative resolved against base",
			raw:  "other.html",
			base: base,
			want: "https://example.com/dir/other.html",
		},
		{
			name: "relative root path",
			raw:  "/root.html",
			base: base,
			want: "https://example.com/root.html",
		},
		{
			name: "uppercase scheme and host",
			raw:  "HTTPS://EXAMPLE.com/Path",
			base: nil,
			want: "https://example.com/Path",
		},
		{
			name: "default http port stripped",
			raw:  "http://example.com:80/a",
			base: nil,
			want: "http://example.com/a",
		},
		{
			name: "default https port stripped",
			raw:  "https://example.com:443/a",
			base: nil,
			want: "https://example.com/a",
		},
		{
			name: "non-default port kept",
			raw:  "http://example.com:8080/a",
			base: nil,
			want: "http://example.com:8080/a",
		},
		{
			name: "fragment removed",
			raw:  "https://example.com/a#section",
			base: nil,
			want: "https://example.com/a",
		},
		{
			name: "empty query removed",
			raw:  "https://example.com/a?",
			base: nil,
			want: "https://example.com/a",
		},
		{
			name: "non-empty query kept as-is",
			raw:  "https://example.com/a?b=2&a=1",
			base: nil,
			want: "https://example.com/a?b=2&a=1",
		},
		{
			name: "dot segments resolved",
			raw:  "https://example.com/a/b/../../c",
			base: nil,
			want: "https://example.com/c",
		},
		{
			name: "dot segments resolved against base",
			raw:  "../sibling.html",
			base: base,
			want: "https://example.com/sibling.html",
		},
		{
			name: "empty path becomes slash",
			raw:  "https://example.com",
			base: nil,
			want: "https://example.com/",
		},
		{
			name:    "empty input is an error",
			raw:     "   ",
			base:    nil,
			wantErr: true,
		},
		{
			name:    "rejected scheme",
			raw:     "ftp://example.com/a",
			base:    nil,
			wantErr: true,
		},
		{
			name:    "javascript scheme rejected",
			raw:     "javascript:void(0)",
			base:    nil,
			wantErr: true,
		},
		{
			name:    "userinfo rejected",
			raw:     "https://user:pass@example.com/a",
			base:    nil,
			wantErr: true,
		},
		{
			name:    "relative without base is an error",
			raw:     "relative/path",
			base:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Normalize(tt.raw, tt.base)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Normalize(%q) = %q, want error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalize(%q) unexpected error: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestSameSite(t *testing.T) {
	tests := []struct {
		name              string
		seedHost          string
		host              string
		includeSubdomains bool
		want              bool
	}{
		{"identical host", "example.com", "example.com", false, true},
		{"www prefix on host", "example.com", "www.example.com", false, true},
		{"www prefix on seed", "www.example.com", "example.com", false, true},
		{"subdomain without flag", "example.com", "blog.example.com", false, false},
		{"subdomain with flag", "example.com", "blog.example.com", true, true},
		{"unrelated host", "example.com", "other.com", false, false},
		{"unrelated host with flag", "example.com", "other.com", true, false},
		{"suffix but not subdomain", "example.com", "notexample.com", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SameSite(tt.seedHost, tt.host, tt.includeSubdomains)
			if got != tt.want {
				t.Errorf("SameSite(%q, %q, %v) = %v, want %v", tt.seedHost, tt.host, tt.includeSubdomains, got, tt.want)
			}
		})
	}
}
