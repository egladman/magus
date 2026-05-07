package cache

import (
	"testing"
)

// TestCompiledGlobAllocsBudget asserts that the hot-path glob matching
// (extension globs and exact paths) is zero-alloc. Any allocation on the
// fast paths indicates a regression (e.g., a string conversion snuck in).
func TestCompiledGlobAllocsBudget(t *testing.T) {
	pats := compileGlobs([]string{
		"web/studio/**/*.ts",
		"web/studio/**/*.tsx",
		"web/studio/package.json",
	})
	paths := []string{
		"web/studio/src/foo.ts",
		"web/studio/package.json",
		"other/bar.ts", // no match
	}

	allocs := testing.AllocsPerRun(100, func() {
		for _, path := range paths {
			for _, p := range pats {
				_ = p.Match(path)
			}
		}
	})
	// Hard gate: extension-glob and exact-path fast paths must be zero-alloc.
	// The doublestar fallback allocates (not exercised here); if allocs > 0
	// the fast-path classification regressed.
	if allocs != 0 {
		t.Fatalf("compiledGlob fast-path Match must be zero-alloc, got %.0f allocs/op\n"+
			"(extension-glob uses HasSuffix+HasPrefix; exact uses == — neither allocates)",
			allocs)
	}
}

func TestCompiledGlobMatchCases(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		// Extension glob with prefix
		{"web/studio/**/*.ts", "web/studio/src/foo.ts", true},
		{"web/studio/**/*.ts", "web/studio/foo.ts", true},
		{"web/studio/**/*.ts", "web/api/foo.ts", false},
		{"web/studio/**/*.ts", "web/studio/src/foo.tsx", false},
		// Extension glob without prefix
		{"**/*.js", "src/foo.js", true},
		{"**/*.js", "src/foo.ts", false},
		// Exact path
		{"web/studio/package.json", "web/studio/package.json", true},
		{"web/studio/package.json", "web/api/package.json", false},
		{"package.json", "package.json", true},
		// Exact path in subdirectory — exact match, not prefix match
		{"web/studio/package.json", "web/studio/src/package.json", false},
	}
	for _, tc := range cases {
		cg := newCompiledGlob(tc.pattern)
		got := cg.Match(tc.path)
		if got != tc.want {
			t.Errorf("newCompiledGlob(%q).Match(%q) = %v, want %v",
				tc.pattern, tc.path, got, tc.want)
		}
	}
}
