package report

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// updateSnapshot regenerates report/testdata/report.md instead of comparing
// against it: go test ./report/ -update.
var updateSnapshot = flag.Bool("update", false, "regenerar el snapshot de testdata")

// snapshotReport is the seeded report with GeneratedAt and the crawl's
// timestamps pinned, so the Markdown snapshot is byte-for-byte stable.
func snapshotReport(t *testing.T) *Report {
	t.Helper()
	r := buildSeeded(t)
	r.GeneratedAt = time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	r.Crawl.StartedAt = time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)
	finished := time.Date(2026, 9, 17, 9, 30, 0, 0, time.UTC)
	r.Crawl.FinishedAt = &finished
	return r
}

func TestMarkdown_Snapshot(t *testing.T) {
	got := Markdown(snapshotReport(t))
	path := filepath.Join("testdata", "report.md")

	if *updateSnapshot {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
		t.Logf("snapshot actualizado: %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v (ejecuta: go test ./report/ -update)", path, err)
	}
	if got != string(want) {
		t.Errorf("Markdown() no coincide con %s (ejecuta: go test ./report/ -update)\n--- got ---\n%s", path, got)
	}
}

func TestMarkdown_AllSections(t *testing.T) {
	md := Markdown(snapshotReport(t))

	if !strings.HasPrefix(md, "# Informe del rastreo #1 — https://example.com/\n") {
		t.Errorf("título inesperado:\n%s", firstLines(md, 3))
	}
	for _, h := range sectionHeadings {
		if !strings.Contains(md, "\n## "+h+"\n") {
			t.Errorf("falta la cabecera ## %s", h)
		}
	}
	// Order of the ## sections must match the spec.
	pos := -1
	for _, h := range sectionHeadings {
		i := strings.Index(md, "\n## "+h+"\n")
		if i <= pos {
			t.Fatalf("sección %q fuera de orden", h)
		}
		pos = i
	}
}

func TestMarkdown_EmptySectionsSayNoData(t *testing.T) {
	r := snapshotReport(t)
	r.Redirects = nil
	r.Errors = nil
	r.DuplicateTitles = nil
	r.TopBroken = nil
	r.Samples = nil
	r.ContentTypes = nil

	md := Markdown(r)
	if n := strings.Count(md, "Sin datos."); n < 6 {
		t.Errorf("secciones vacías: %d veces \"Sin datos.\", want >= 6\n%s", n, md)
	}
}

func TestMarkdown_ThousandsSeparator(t *testing.T) {
	r := snapshotReport(t)
	r.Summary.Total = 1234567
	md := Markdown(r)
	if !strings.Contains(md, "1.234.567") {
		t.Errorf("no se ha formateado 1234567 como 1.234.567")
	}
}

func TestThousands(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0"}, {7, "7"}, {999, "999"}, {1000, "1.000"}, {12345, "12.345"},
		{999999, "999.999"}, {1000000, "1.000.000"}, {1234567890, "1.234.567.890"},
		{-4321, "-4.321"},
	}
	for _, tc := range tests {
		if got := thousands(tc.in); got != tc.want {
			t.Errorf("thousands(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
