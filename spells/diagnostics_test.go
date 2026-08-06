package spells_test

import (
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verbatim `hadolint -f gnu` output. It carries the optional program prefix and no
// column, and puts the rule code ahead of the severity.
func TestParseDiagnosticsHadolintGNU(t *testing.T) {
	got := spells.ParseDiagnostics(spells.DiagnosticGNU, "",
		"hadolint:Dockerfile:1: DL3006 warning: Always tag the version of an image explicitly\n"+
			"hadolint:Dockerfile:2: DL3015 info: Avoid additional packages\n")

	require.Len(t, got, 2)
	assert.Equal(t, spells.Diagnostic{
		File: "Dockerfile", Line: 1, Severity: "warning", Code: "DL3006",
		Message: "Always tag the version of an image explicitly",
	}, got[0], "the program prefix is not the file")
	assert.Equal(t, 2, got[1].Line)
	assert.Equal(t, "info", got[1].Severity)
}

// Verbatim `shellcheck --format=gcc` output: no program prefix, a column, and the code
// trailing in brackets rather than leading.
func TestParseDiagnosticsShellcheckGCC(t *testing.T) {
	got := spells.ParseDiagnostics(spells.DiagnosticGNU, "",
		"bad.sh:2:4: note: Double quote to prevent globbing and word splitting. [SC2086]\n")

	require.Len(t, got, 1)
	assert.Equal(t, spells.Diagnostic{
		File: "bad.sh", Line: 2, Col: 4, Severity: "note", Code: "SC2086",
		Message: "Double quote to prevent globbing and word splitting.",
	}, got[0], "a trailing bracketed code is lifted out of the message")
}

// Lines a tool interleaves with its findings are skipped rather than guessed at: a
// wrong structural read claims a file and a line that do not exist.
func TestParseDiagnosticsSkipsProse(t *testing.T) {
	got := spells.ParseDiagnostics(spells.DiagnosticGNU, "",
		"Scanning 3 files...\nbad.sh:2:4: note: real finding\nDone. 1 issue found.\n")

	require.Len(t, got, 1)
	assert.Equal(t, "real finding", got[0].Message)
}

// A Windows path keeps its drive letter: the optional program slot must not swallow it.
func TestParseDiagnosticsKeepsAWindowsDriveLetter(t *testing.T) {
	got := spells.ParseDiagnostics(spells.DiagnosticGNU, "", `C:\src\main.go:12:5: error: boom`)

	require.Len(t, got, 1)
	assert.Equal(t, `C:\src\main.go`, got[0].File)
	assert.Equal(t, 12, got[0].Line)
}

// An undeclared format parses nothing, so a caller cannot accidentally read prose as
// structure by forgetting the declaration.
func TestParseDiagnosticsIgnoresAnUndeclaredFormat(t *testing.T) {
	assert.Empty(t, spells.ParseDiagnostics(spells.DiagnosticNone, "", "bad.sh:2:4: note: msg"))
}

// Verbatim golangci-lint v2 default text output - v2 dropped --out-format, so this is
// the unflagged default. No severity word: the linter name trails in parens instead of
// a GNU severity, so gnuLead doesn't fire and the whole tail stays the message.
func TestParseDiagnosticsGolangciLintDefault(t *testing.T) {
	got := spells.ParseDiagnostics(spells.DiagnosticGNU, "",
		"main.go:12:19: printf: fmt.Sprintf format %s has arg 5 of wrong type int (govet)\n")

	require.Len(t, got, 1)
	assert.Equal(t, spells.Diagnostic{
		File: "main.go", Line: 12, Col: 19,
		Message: "printf: fmt.Sprintf format %s has arg 5 of wrong type int (govet)",
	}, got[0], "no severity word means Severity/Code stay empty and the tail is kept whole")
}

// Verbatim `cargo clippy --message-format=short` output.
func TestParseDiagnosticsCargoClippyShort(t *testing.T) {
	got := spells.ParseDiagnostics(spells.DiagnosticGNU, "",
		"src/main.rs:7:8: error: length comparison to zero: help: using `is_empty` is clearer and more explicit: `v.is_empty()`\n")

	require.Len(t, got, 1)
	assert.Equal(t, spells.Diagnostic{
		File: "src/main.rs", Line: 7, Col: 8, Severity: "error",
		Message: "length comparison to zero: help: using `is_empty` is clearer and more explicit: `v.is_empty()`",
	}, got[0])
}

// Verbatim `buf lint` default text output (buf 1.58.0), no space after the location
// colon. buf 1.58.0 has no `gcc` --error-format value - the real choices are text,
// json, msvs, junit, github-actions, gitlab-code-quality, config-ignore-yaml - and
// this is the default, unflagged one.
func TestParseDiagnosticsBufDefaultText(t *testing.T) {
	got := spells.ParseDiagnostics(spells.DiagnosticGNU, "",
		`proto/bad.proto:3:1:Files with package "bad" must be within a directory "bad" relative to root but were in directory "proto".`+"\n")

	require.Len(t, got, 1)
	assert.Equal(t, spells.Diagnostic{
		File: "proto/bad.proto", Line: 3, Col: 1,
		Message: `Files with package "bad" must be within a directory "bad" relative to root but were in directory "proto".`,
	}, got[0], "no space after the location colon still parses")
}

// Verbatim `biome check --reporter=concise` output (biome 2.5.7). Each line carries a
// leading severity glyph (! for a warning, U+00D7 MULTIPLICATION SIGN for an error)
// before the location, stripped rather than swallowed into File.
func TestParseDiagnosticsBiomeConcise(t *testing.T) {
	got := spells.ParseDiagnostics(spells.DiagnosticGNU, "",
		"! bad.js:1:10: lint/correctness/noUnusedVariables: This function foo is unused.\n"+
			"× bad.js:3:7: lint/suspicious/noAssignInExpressions: The assignment should not be in an expression.\n")

	require.Len(t, got, 2)
	assert.Equal(t, spells.Diagnostic{
		File: "bad.js", Line: 1, Col: 10,
		Message: "lint/correctness/noUnusedVariables: This function foo is unused.",
	}, got[0], "the leading glyph is not part of the file")
	assert.Equal(t, "bad.js", got[1].File)
	assert.Equal(t, 3, got[1].Line)
}

// Verbatim `tsc` output (typescript 5.8.3 and 7.0.2 both): a parenthesized location,
// not GNU by any flag. tsc's only formatting flag is --pretty, and it just toggles
// color (confirmed against --help --all). This is the motivating case for
// DiagnosticCustom: a spell author names capture groups instead of waiting on a magus
// release to add a format tsc still wouldn't fit.
func TestParseDiagnosticsCustomPatternTSC(t *testing.T) {
	pattern := `^(?P<file>.+?)\((?P<line>\d+),(?P<col>\d+)\): (?P<severity>error|warning) (?P<code>TS\d+): (?P<message>.*)$`
	got := spells.ParseDiagnostics(spells.DiagnosticCustom, pattern,
		"bad.ts(4,5): error TS2345: Argument of type 'string' is not assignable to parameter of type 'number'.\n")

	require.Len(t, got, 1)
	assert.Equal(t, spells.Diagnostic{
		File: "bad.ts", Line: 4, Col: 5, Severity: "error", Code: "TS2345",
		Message: "Argument of type 'string' is not assignable to parameter of type 'number'.",
	}, got[0])
}

// A custom pattern with no named groups still matches per line - decode is what
// enforces file/line, not parseCustom - so every field on the Diagnostic stays zero
// rather than the line being dropped.
func TestParseDiagnosticsCustomPatternUnnamedGroupsAreIgnored(t *testing.T) {
	got := spells.ParseDiagnostics(spells.DiagnosticCustom, `^(\S+):(\d+): (.*)$`, "bad.ts:4: boom\n")

	require.Len(t, got, 1)
	assert.Equal(t, spells.Diagnostic{}, got[0], "no named groups means every field stays zero")
}

// An uncompilable pattern - decode should have rejected this before it ever reaches
// here - yields nothing rather than panicking, the same tolerant fallback as an
// undeclared format.
func TestParseDiagnosticsCustomPatternBadRegexYieldsNothing(t *testing.T) {
	assert.Empty(t, spells.ParseDiagnostics(spells.DiagnosticCustom, `(unclosed`, "bad.ts:4: boom"))
}

func TestLikelyMisconfigured(t *testing.T) {
	cases := []struct {
		name   string
		f      spells.DiagnosticFormat
		failed bool
		output string
		found  []spells.Diagnostic
		want   bool
	}{
		{
			name: "failed, declared, wrote output, nothing extracted - the anomaly",
			f:    spells.DiagnosticGNU, failed: true, output: "some prose\n", found: nil,
			want: true,
		},
		{
			name: "passing run with zero diagnostics is normal, not a misconfiguration",
			f:    spells.DiagnosticGNU, failed: false, output: "", found: nil,
			want: false,
		},
		{
			name: "failed but no format declared - nothing to have been misconfigured",
			f:    spells.DiagnosticNone, failed: true, output: "some prose\n", found: nil,
			want: false,
		},
		{
			name: "failed and declared, but the tool wrote nothing at all",
			f:    spells.DiagnosticGNU, failed: true, output: "", found: nil,
			want: false,
		},
		{
			name: "failed and declared, and diagnostics WERE extracted - working as intended",
			f:    spells.DiagnosticGNU, failed: true, output: "bad.sh:2:4: note: msg\n",
			found: []spells.Diagnostic{{File: "bad.sh", Line: 2}},
			want:  false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, spells.LikelyMisconfigured(c.f, c.failed, c.output, c.found))
		})
	}
}
