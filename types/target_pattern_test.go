package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// targetNames is a stand-in for a registered target set, deliberately including the bare
// name "generate" alongside the "-generate" family: the two are matched by different rules
// and conflating them is the mistake these cases exist to catch.
var targetNames = []string{
	"generate", "md-generate", "content-generate", "site-generate",
	"build", "go-build", "image-build", "ci",
}

func TestMatchTargetPatterns(t *testing.T) {
	for _, tc := range []struct {
		name     string
		patterns []string
		want     []string
	}{{
		name:     "glob matches the family but not the bare name",
		patterns: []string{"*-generate"},
		want:     []string{"content-generate", "md-generate", "site-generate"},
	}, {
		name:     "suffix shorthand reads a bare word as -word",
		patterns: []string{"generate"},
		want:     []string{"content-generate", "md-generate", "site-generate"},
	}, {
		// The whole reason ctx.glob("generate") is safe to write inside the `generate`
		// target: widening the shorthand to match bare names would make it self-recursive.
		name:     "suffix shorthand never matches the bare name itself",
		patterns: []string{"build"},
		want:     []string{"go-build", "image-build"},
	}, {
		name:     "negation subtracts one name from the family",
		patterns: []string{"*-generate", "!site-generate"},
		want:     []string{"content-generate", "md-generate"},
	}, {
		name:     "negation is order independent",
		patterns: []string{"!site-generate", "*-generate"},
		want:     []string{"content-generate", "md-generate"},
	}, {
		// A negation is a NAME or a glob, never suffix shorthand. Were it shorthand it
		// would compile to ^.*-site-generate$ and quietly subtract nothing.
		name:     "negation of a bare name is exact, not suffix shorthand",
		patterns: []string{"*-generate", "!md-generate"},
		want:     []string{"content-generate", "site-generate"},
	}, {
		name:     "negation may itself be a glob",
		patterns: []string{"*-generate", "!*-build", "!site-*"},
		want:     []string{"content-generate", "md-generate"},
	}, {
		name:     "negation with no include selects nothing, never everything",
		patterns: []string{"!site-generate"},
		want:     nil,
	}, {
		name:     "overlapping includes yield each name once",
		patterns: []string{"*-generate", "generate"},
		want:     []string{"content-generate", "md-generate", "site-generate"},
	}, {
		// The dash is what excludes the bare name, not the wildcard: "*generate" is an
		// ordinary glob ending in "generate", so it DOES match a target called exactly
		// that, where the "generate" shorthand and "*-generate" both do not.
		name:     "a glob without the dash also matches the bare name",
		patterns: []string{"*generate"},
		want:     []string{"content-generate", "generate", "md-generate", "site-generate"},
	}, {
		name:     "an authored regexp is matched literally, not compiled",
		patterns: []string{"^(?!site-).*-generate$"},
		want:     nil,
	}, {
		name:     "no patterns select nothing",
		patterns: nil,
		want:     nil,
	}, {
		name:     "a pattern matching nothing is not an error",
		patterns: []string{"*-nonexistent"},
		want:     nil,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, MatchTargetPatterns(targetNames, tc.patterns))
		})
	}
}

// TestMatchTargetPatternsSorted locks the ordering contract: callers iterate a Go map to
// produce names, so an unsorted result would make the dispatch order vary run to run.
func TestMatchTargetPatternsSorted(t *testing.T) {
	got := MatchTargetPatterns([]string{"z-generate", "a-generate", "m-generate"}, []string{"*-generate"})
	assert.Equal(t, []string{"a-generate", "m-generate", "z-generate"}, got)
}

// TestMatchTargetPatternsDocumentedExamples pins every worked example in
// docs/concepts/dependencies.md ("Every form, against one target set") against the real
// matcher, using that section's exact target set. The page states a resolved set per
// pattern list; a doc that asserts behavior should fail the build when it drifts from it,
// not quietly mislead the next person who copies it.
func TestMatchTargetPatternsDocumentedExamples(t *testing.T) {
	// The five targets the documented examples declare, verbatim.
	docNames := []string{"md-generate", "site-generate", "vendor-generate", "go-build", "generate"}

	for _, tc := range []struct {
		umbrella string
		patterns []string
		want     []string
	}{
		{"all_generate", []string{"*-generate"}, []string{"md-generate", "site-generate", "vendor-generate"}},
		{"all_generate_shorthand", []string{"generate"}, []string{"md-generate", "site-generate", "vendor-generate"}},
		{"generate_fast", []string{"*-generate", "!site-generate"}, []string{"md-generate", "vendor-generate"}},
		{"generate_first_party", []string{"*-generate", "!vendor-*"}, []string{"md-generate", "site-generate"}},
		{"generate_fast_reordered", []string{"!site-generate", "*-generate"}, []string{"md-generate", "vendor-generate"}},
		{"everything", []string{"*-generate", "*-build"}, []string{"go-build", "md-generate", "site-generate", "vendor-generate"}},
		{"nothing_at_all", []string{"!site-generate"}, nil},
		{"negation_is_exact", []string{"*-generate", "!generate"}, []string{"md-generate", "site-generate", "vendor-generate"}},
		{"regex_does_nothing", []string{"^(?!site-).*-generate$"}, nil},
		{"future_family", []string{"*-lint"}, nil},
	} {
		t.Run(tc.umbrella, func(t *testing.T) {
			assert.Equal(t, tc.want, MatchTargetPatterns(docNames, tc.patterns),
				"docs/concepts/dependencies.md documents this exact result for %s", tc.umbrella)
		})
	}
}
