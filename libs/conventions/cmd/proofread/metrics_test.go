package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func metricsOf(t *testing.T, args []string, stdin string) []measured {
	t.Helper()

	var stdout, stderr bytes.Buffer
	if code := run(args, strings.NewReader(stdin), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}

	var out []measured
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatal(err)
	}

	return out
}

func TestMetricsMeasuresEachFileInArgumentOrder(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md")

	if err := os.WriteFile(a, []byte("# Title\n\nThe cat sat. The cat sat on the mat today.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(b, []byte("Run `magus run` now.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := metricsOf(t, []string{"reference", "-metrics", a, b}, "")

	if len(got) != 2 || got[0].Source != a || got[0].Words != 10 || got[0].Sentences != 2 ||
		got[1].Source != b || got[1].Words != 3 || got[1].Syllables != 3 {
		t.Errorf("metrics = %+v", got)
	}
}

func TestMetricsMeasuresStdinAndSymbols(t *testing.T) {
	got := metricsOf(t, []string{"review-reply", "-metrics"}, "The map races. It needs a lock.")
	if len(got) != 1 || got[0].Source != "review-reply" || got[0].Sentences != 2 || got[0].SentenceWordsMean != 3.5 {
		t.Errorf("reply metrics = %+v", got)
	}

	symbols := `[{"node":"n","source":"a.go:3","language":"go","name":"F","kind":"function","owner":"","doc":"F runs it."}]`

	got = metricsOf(t, []string{"doc-comment", "-metrics"}, symbols)
	if len(got) != 1 || got[0].Source != "a.go:3" || got[0].Words != 3 {
		t.Errorf("doc metrics = %+v", got)
	}
}

func TestMetricsRefusesInputItCannotRead(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"a path for stdin":     {[]string{"review-reply", "-metrics", "x.md"}, "proofread: a review-reply is read from stdin, not from a path"},
		"a path for symbols":   {[]string{"doc-comment", "-metrics", "x.md"}, "proofread: a doc-comment is read from stdin, not from a path"},
		"a file that is gone":  {[]string{"reference", "-metrics", "missing.md"}, "proofread: read missing.md: open missing.md: no such file or directory"},
		"symbols that are bad": {[]string{"doc-comment", "-metrics"}, "proofread: read symbols: EOF"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(tc.args, strings.NewReader(""), &stdout, &stderr)
			if code != 1 || strings.TrimSpace(stderr.String()) != tc.want {
				t.Errorf("exit %d, stderr %q, want 1 and %q", code, stderr.String(), tc.want)
			}
		})
	}
}
