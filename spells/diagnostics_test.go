package spells

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Verbatim `hadolint -f gnu` output. It carries the optional program prefix and no
// column, and puts the rule code ahead of the severity.
func TestParseDiagnosticsHadolintGNU(t *testing.T) {
	got := ParseDiagnostics(DiagnosticGNU,
		"hadolint:Dockerfile:1: DL3006 warning: Always tag the version of an image explicitly\n"+
			"hadolint:Dockerfile:2: DL3015 info: Avoid additional packages\n")

	require.Len(t, got, 2)
	assert.Equal(t, Diagnostic{
		File: "Dockerfile", Line: 1, Severity: "warning", Code: "DL3006",
		Message: "Always tag the version of an image explicitly",
	}, got[0], "the program prefix is not the file")
	assert.Equal(t, 2, got[1].Line)
	assert.Equal(t, "info", got[1].Severity)
}

// Verbatim `shellcheck --format=gcc` output: no program prefix, a column, and the code
// trailing in brackets rather than leading.
func TestParseDiagnosticsShellcheckGCC(t *testing.T) {
	got := ParseDiagnostics(DiagnosticGNU,
		"bad.sh:2:4: note: Double quote to prevent globbing and word splitting. [SC2086]\n")

	require.Len(t, got, 1)
	assert.Equal(t, Diagnostic{
		File: "bad.sh", Line: 2, Col: 4, Severity: "note", Code: "SC2086",
		Message: "Double quote to prevent globbing and word splitting.",
	}, got[0], "a trailing bracketed code is lifted out of the message")
}

// Lines a tool interleaves with its findings are skipped rather than guessed at: a
// wrong structural read claims a file and a line that do not exist.
func TestParseDiagnosticsSkipsProse(t *testing.T) {
	got := ParseDiagnostics(DiagnosticGNU,
		"Scanning 3 files...\nbad.sh:2:4: note: real finding\nDone. 1 issue found.\n")

	require.Len(t, got, 1)
	assert.Equal(t, "real finding", got[0].Message)
}

// A Windows path keeps its drive letter: the optional program slot must not swallow it.
func TestParseDiagnosticsKeepsAWindowsDriveLetter(t *testing.T) {
	got := ParseDiagnostics(DiagnosticGNU, `C:\src\main.go:12:5: error: boom`)

	require.Len(t, got, 1)
	assert.Equal(t, `C:\src\main.go`, got[0].File)
	assert.Equal(t, 12, got[0].Line)
}

// An undeclared format parses nothing, so a caller cannot accidentally read prose as
// structure by forgetting the declaration.
func TestParseDiagnosticsIgnoresAnUndeclaredFormat(t *testing.T) {
	assert.Empty(t, ParseDiagnostics(DiagnosticNone, "bad.sh:2:4: note: msg"))
}
