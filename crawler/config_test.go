package crawler

import (
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.MaxPages != 500 {
		t.Errorf("MaxPages = %d, want 500", d.MaxPages)
	}
	if d.MaxDepth != 10 {
		t.Errorf("MaxDepth = %d, want 10", d.MaxDepth)
	}
	if d.Concurrency != 4 {
		t.Errorf("Concurrency = %d, want 4", d.Concurrency)
	}
	if d.Delay != 500*time.Millisecond {
		t.Errorf("Delay = %v, want 500ms", d.Delay)
	}
	if d.Timeout != 15*time.Second {
		t.Errorf("Timeout = %v, want 15s", d.Timeout)
	}
	if d.MaxBodyBytes != 2*1024*1024 {
		t.Errorf("MaxBodyBytes = %d, want %d", d.MaxBodyBytes, 2*1024*1024)
	}
	if d.UserAgent != "EANBot/0.1 (+https://xavi.net)" {
		t.Errorf("UserAgent = %q", d.UserAgent)
	}
	if d.RobotsToken != "eanbot" {
		t.Errorf("RobotsToken = %q", d.RobotsToken)
	}
	if !d.UseSitemaps {
		t.Error("UseSitemaps should default to true")
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want []string
	}{
		{
			name: "valid config",
			cfg: Config{
				Seed: "https://example.com/", MaxPages: 1, MaxDepth: 0,
				Concurrency: 1, Delay: 0,
			},
			want: nil,
		},
		{
			name: "missing seed",
			cfg:  Config{MaxPages: 1, Concurrency: 1},
			want: []string{"la semilla es obligatoria"},
		},
		{
			name: "invalid seed scheme",
			cfg:  Config{Seed: "ftp://example.com", MaxPages: 1, Concurrency: 1},
			want: []string{"la semilla debe ser una URL http o https absoluta"},
		},
		{
			name: "relative seed",
			cfg:  Config{Seed: "/relative", MaxPages: 1, Concurrency: 1},
			want: []string{"la semilla debe ser una URL http o https absoluta"},
		},
		{
			name: "everything invalid",
			cfg: Config{
				Seed: "", MaxPages: 0, MaxDepth: -1, Concurrency: 0,
				Delay: -1 * time.Second,
			},
			want: []string{
				"la semilla es obligatoria",
				"max_pages debe ser mayor que 0",
				"max_depth no puede ser negativo",
				"concurrency debe ser mayor que 0",
				"delay_ms no puede ser negativo",
			},
		},
		{
			name: "invalid header name",
			cfg: Config{
				Seed: "https://example.com/", MaxPages: 1, Concurrency: 1,
				Headers: map[string]string{"x ean": "abc"},
			},
			want: []string{`cabecera no válida: "x ean"`},
		},
		{
			name: "empty header name",
			cfg: Config{
				Seed: "https://example.com/", MaxPages: 1, Concurrency: 1,
				Headers: map[string]string{"": "abc"},
			},
			want: []string{`cabecera no válida: ""`},
		},
		{
			name: "header value with newline",
			cfg: Config{
				Seed: "https://example.com/", MaxPages: 1, Concurrency: 1,
				Headers: map[string]string{"X-Ean-Client": "abc\ndef"},
			},
			want: []string{`valor de cabecera no válido: "X-Ean-Client"`},
		},
		{
			name: "header value with carriage return",
			cfg: Config{
				Seed: "https://example.com/", MaxPages: 1, Concurrency: 1,
				Headers: map[string]string{"X-Ean-Client": "abc\rdef"},
			},
			want: []string{`valor de cabecera no válido: "X-Ean-Client"`},
		},
		{
			name: "valid custom header",
			cfg: Config{
				Seed: "https://example.com/", MaxPages: 1, Concurrency: 1,
				Headers: map[string]string{"X-Ean-Client": "abc"},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.Validate()
			if len(got) != len(tt.want) {
				t.Fatalf("Validate() = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("Validate()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestConfigWithDefaults(t *testing.T) {
	cfg := Config{Seed: "https://example.com/"}
	filled := cfg.WithDefaults()

	d := Defaults()
	if filled.MaxPages != d.MaxPages || filled.MaxDepth != d.MaxDepth ||
		filled.Concurrency != d.Concurrency ||
		filled.Timeout != d.Timeout || filled.MaxBodyBytes != d.MaxBodyBytes ||
		filled.UserAgent != d.UserAgent || filled.RobotsToken != d.RobotsToken {
		t.Errorf("WithDefaults() did not fill zero fields: %+v", filled)
	}
	if filled.Seed != cfg.Seed {
		t.Errorf("Seed = %q, want unchanged %q", filled.Seed, cfg.Seed)
	}

	// Delay is like the booleans: zero is a legitimate explicit value (no
	// courtesy delay), not a sentinel for "unset", so it must never be
	// overridden by WithDefaults.
	if filled.Delay != 0 {
		t.Errorf("Delay = %v, want 0 (left untouched)", filled.Delay)
	}

	// Booleans must never be touched, even though their zero value (false)
	// differs from UseSitemaps' documented default (true).
	explicit := Config{Seed: "https://example.com/", UseSitemaps: false, IncludeSubdomains: true}
	filledExplicit := explicit.WithDefaults()
	if filledExplicit.UseSitemaps != false {
		t.Error("WithDefaults() must not touch UseSitemaps")
	}
	if filledExplicit.IncludeSubdomains != true {
		t.Error("WithDefaults() must not touch IncludeSubdomains")
	}

	// Non-zero values must be preserved.
	custom := Config{Seed: "https://example.com/", MaxPages: 42}
	if got := custom.WithDefaults().MaxPages; got != 42 {
		t.Errorf("MaxPages = %d, want 42 (unchanged)", got)
	}
}
