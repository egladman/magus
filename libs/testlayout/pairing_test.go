package testlayout

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestPairing runs testpair over a package holding every shape the rule decides:
// exact pairs, a build-suffix pair, a platform family, a source file carrying the
// same suffix as its test, narrowings, a benchmark file, and the conventional
// names the standard library exempts, which pair here like any other.
//
// concurrency_test.go is an external test package, so its pass holds no source
// files and the rule only reaches the right answer because the sibling listing is
// read off disk.
func TestPairing(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Pairing, "pairing")
}

// TestPairingIgnoredFiles checks the files a build constraint keeps out of every
// pass on this platform: an unpaired one is still reported, at its package clause,
// and a paired one is not.
//
// analysistest reads `// want` only from loaded files, so this asserts on the
// diagnostics it returns and discards the "unexpected diagnostic" errors those
// produce.
func TestPairingIgnoredFiles(t *testing.T) {
	results := analysistest.Run(discard{}, analysistest.TestData(), Pairing, "tagged")

	var reported []string

	for _, r := range results {
		for _, d := range r.Diagnostics {
			posn := r.Pass.Fset.Position(d.Pos)
			reported = append(reported, fmt.Sprintf("%s:%d", filepath.Base(posn.Filename), posn.Line))

			if !strings.HasPrefix(d.Message, "orphan_test.go has no source file of the same name") {
				t.Errorf("unexpected message: %s", d.Message)
			}
		}
	}

	slices.Sort(reported)
	reported = slices.Compact(reported)

	if want := []string{"orphan_test.go:3"}; !slices.Equal(reported, want) {
		t.Errorf("reported %v, want %v", reported, want)
	}
}

type discard struct{}

func (discard) Errorf(string, ...any) {}

// TestPairingMessages pins the decision for each name against a fixed listing,
// including the order the checks run in.
func TestPairingMessages(t *testing.T) {
	sources := map[string]bool{
		"resolver":              true,
		"resolver_cache_purego": true,
		"bytes":                 true,
		"tree_linux":            true,
		"tree_darwin":           true,
		"relay_unix":            true,
		"widget":                true,
	}

	cases := []struct {
		name string
		want string
	}{
		{"resolver_test.go", ""},
		// Two build suffixes in a row, so trimming has to loop.
		{"bytes_js_wasm_test.go", ""},
		// The exact pair wins before trimming; trimmed first, it would narrow resolver.go.
		{"resolver_cache_purego_test.go", ""},
		{"tree_test.go", ""},
		{"relay_unix_test.go", ""},
		{"resolver_edge_cases_test.go", "resolver_edge_cases_test.go narrows resolver.go; these tests belong in resolver_test.go"},
		// nearestSource iterates twice: neither widget_cache_entry nor widget_cache is a source.
		{"widget_cache_entry_test.go", "widget_cache_entry_test.go narrows widget.go"},
		{"resolver_bench_test.go", "resolver_bench_test.go narrows resolver.go"},
		{"bench_test.go", "bench_test.go keeps benchmarks apart from the tests of the file they measure"},
		{"sweep_bench_test.go", "sweep_bench_test.go keeps benchmarks apart"},
		{"example_test.go", "example_test.go has no source file of the same name; move its tests into the _test.go of the file they exercise, or move the code it owns into example.go"},
		{"export_test.go", "export_test.go has no source file of the same name"},
		{"main_test.go", "main_test.go has no source file of the same name"},
		{"fuzz_test.go", "fuzz_test.go has no source file of the same name"},
		{"internal_test.go", "internal_test.go has no source file of the same name"},
		{"concurrency_test.go", "concurrency_test.go has no source file of the same name"},
		// The whole stem is a build suffix: nothing pairs and nothing narrows.
		{"unix_test.go", "unix_test.go has no source file of the same name"},
	}

	for _, tc := range cases {
		got := pairing(tc.name, sources)
		if tc.want == "" && got != "" {
			t.Errorf("pairing(%q) = %q, want it to pair", tc.name, got)
		}

		if tc.want != "" && !strings.HasPrefix(got, tc.want) {
			t.Errorf("pairing(%q) = %q, want prefix %q", tc.name, got, tc.want)
		}
	}
}

// TestPairingUnreadableDirectory pins that a directory the analyzer cannot list
// reports every test file as unpaired rather than failing or staying silent.
func TestPairingUnreadableDirectory(t *testing.T) {
	sources := sourceNames(filepath.Join(t.TempDir(), "absent"))
	if got := pairing("widget_test.go", sources); !strings.HasPrefix(got, "widget_test.go has no source file") {
		t.Errorf("pairing over an unreadable directory = %q", got)
	}
}
