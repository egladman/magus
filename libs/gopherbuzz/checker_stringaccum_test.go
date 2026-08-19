package buzz

import (
	"testing"

	"github.com/egladman/magus/libs/diagnostics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// warningsOf type-checks src and returns only the non-fatal diagnostics.
func warningsOf(t *testing.T, src string) []typeError {
	t.Helper()
	prog, err := ParseEmbedded(src)
	require.NoError(t, err)
	errs, warnings := checkWithGlobals(prog, nil, nil, nil, nil, nil, nil)
	require.Empty(t, errs, "the fixture must type-check cleanly, or the warning is not what failed it")
	return warnings
}

// TestStringAccumulationWarnsInEveryLoopForm covers the shape BZZ3002 exists for. Each
// body rebuilds a str from itself, which copies the whole buffer per iteration and - on
// the nanbox build - pins every copy for the life of the process.
func TestStringAccumulationWarnsInEveryLoopForm(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"while", `var s = ""; var i = 0; while (i < 3) { s = s + "x"; i = i + 1; }`},
		{"foreach", `var s = ""; foreach (p in ["a"]) { s = s + p; }`},
		{"for", `var s = ""; for (i: int = 0; i < 3; i = i + 1) { s = s + "x"; }`},
		{"do until", `var s = ""; var i = 0; do { s = s + "x"; i = i + 1; } until (i > 2)`},
		{"accumulator on the right", `var s = ""; foreach (p in ["a"]) { s = p + s; }`},
		{"deep in the concat spine", `var s = ""; foreach (p in ["a"]) { s = "<" + s + p + ">"; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := warningsOf(t, tc.src)
			require.Len(t, got, 1)
			assert.Equal(t, diagnostics.Code("BZZ3002"), got[0].Code)
			assert.Equal(t, SeverityWarning, got[0].Severity, "BZZ3002 must never fail a compile")
			assert.Contains(t, got[0].Msg, `"s"`)
		})
	}
}

// TestStringAccumulationStaysQuiet pins the boundaries. Each of these rebuilds or
// concatenates something without growing a string across iterations, and warning on them
// would make the check noise rather than signal.
func TestStringAccumulationStaysQuiet(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"outside any loop", `var s = ""; s = s + "x";`},
		{"int counter", `var i = 0; foreach (p in ["a"]) { i = i + 1; }`},
		{"rebuilt but not grown", `var s = "ab"; foreach (p in ["a"]) { s = s.trim(); }`},
		{"a different accumulator", `var s = ""; var t = "x"; foreach (p in ["a"]) { s = t + p; }`},
		{"list append, the fix itself", `final parts: mut [str] = mut [<str>]; foreach (p in ["a"]) { parts.append(p); }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Empty(t, warningsOf(t, tc.src))
		})
	}
}

// TestStringAccumulationReportedOnce guards the speculative return-inference pass, which
// walks function bodies ahead of the real check purely to learn types. It discards errors;
// it must discard warnings too, or every accumulation inside a function is reported twice.
func TestStringAccumulationReportedOnce(t *testing.T) {
	got := warningsOf(t, `fun build(xs: [str]) > str { var s = ""; foreach (x in xs) { s = s + x; } return s; }`)
	require.Len(t, got, 1, "the speculative pass must not double-report")
}
