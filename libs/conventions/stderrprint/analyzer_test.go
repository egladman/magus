package stderrprint

import (
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"
	"golang.org/x/tools/go/analysis/analysistest"
)

var sources = Options{
	UsagePattern:   `(?i)(usage|help)$`,
	UsageFields:    []string{"Usage"},
	CalleePackages: []string{"cli"},
	Display:        []string{"cli/tty", "cli.newDisplay"},
}

func TestAnalyzer(t *testing.T) {
	analyzer, err := New(sources)
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "cli")
}

// An allowed file is never reported for its writes, and an entry naming one file is
// reported once that file has none left.
func TestAnalyzerRatchetsAllowedFiles(t *testing.T) {
	opts := sources
	opts.Allow = []AllowEntry{
		{File: "staged/staged.go", Reason: "staged"},
		{File: "staged/clean.go", Reason: "staged"},
	}
	analyzer, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "staged")
}

// New refuses an allow entry naming a file that has moved.
func TestNewRejectsDeadScope(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "cmd/magus/job.go")
	opts := sources
	opts.Module = "example.com/m"
	opts.Allow = []AllowEntry{{File: "cmd/magus/job.go", Reason: "staged"}}
	if _, err := New(opts); err != nil {
		t.Fatal(err)
	}
	opts.Allow[0].File = "cmd/magus/jobs.go"
	if _, err := New(opts); err == nil || !strings.Contains(err.Error(), `stderrprint: allow pattern "cmd/magus/jobs.go"`) {
		t.Fatalf("want an error naming the dead pattern, got %v", err)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Options)
	}{
		{"no way to print help", func(o *Options) { o.UsagePattern, o.UsageFields = "", nil }},
		{"a bad usage pattern", func(o *Options) { o.UsagePattern = "(" }},
		{"an allow entry with no reason", func(o *Options) { o.Allow = []AllowEntry{{File: "cli/*.go"}} }},
		{"an allow entry with no file", func(o *Options) { o.Allow = []AllowEntry{{Reason: "staged"}} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := sources
			tc.edit(&opts)
			if _, err := New(opts); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}
