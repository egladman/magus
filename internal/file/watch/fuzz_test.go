package watch

import (
	"strings"
	"testing"

	"github.com/egladman/magus/types"
)

// FuzzParsePattern probes the buildx-style ignore-entry parser. The
// invariant is "no panic": every input either returns a typed error
// or a well-formed IgnorePattern that ValidatePattern accepts.
func FuzzParsePattern(f *testing.F) {
	for _, seed := range []string{
		"",
		"type=glob,pattern=**/scratch/*",
		"type=regex,pattern=\\.tmp$",
		"type=literal,pattern=node_modules",
		"type=glob,pattern=foo\\,bar",
		"pattern=foo,type=glob",
		"type=,pattern=",
		"unknown=foo",
		"type=glob,pattern={invalid",
		"\xff\x00",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		// Skip inputs containing NULs: the parser does not pretend to
		// be NUL-safe (CLI/YAML never carries them) and treating
		// them as out-of-scope keeps the fuzzer focused on real bugs.
		if strings.IndexByte(s, 0) >= 0 {
			t.Skip()
		}
		got, err := ParsePattern(s)
		if err != nil {
			return
		}
		if vErr := ValidatePattern(got); vErr != nil {
			t.Fatalf("ParsePattern(%q) = %+v but ValidatePattern rejects it: %v", s, got, vErr)
		}
	})
}

// FuzzIgnorePatternsMatch probes the compiled-pattern matcher with
// arbitrary regex/glob/literal patterns and arbitrary paths. The
// invariant is "no panic across pattern types"; an invalid regex must
// be silently dropped (not crash the matcher).
func FuzzIgnorePatternsMatch(f *testing.F) {
	f.Add("glob", "**/scratch/*", "/repo/api/scratch/foo")
	f.Add("regex", `\.tmp$`, "/repo/api/main.tmp")
	f.Add("literal", "node_modules", "/repo/web/node_modules/foo")
	f.Add("regex", "(", "/repo/api/main.go") // invalid regex
	f.Add("glob", "[", "/repo/api/main.go")  // invalid glob
	f.Fuzz(func(t *testing.T, typ, pat, abs string) {
		if strings.IndexByte(typ+pat+abs, 0) >= 0 {
			t.Skip()
		}
		match := IgnorePatterns("/repo", []types.IgnorePattern{{Type: types.PatternType(typ), Pattern: pat}})
		_ = match(abs) // panic = test failure
	})
}
