package spellruntime

import (
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A spell declaring no tools keeps the conservative default, so every spell that says
// nothing about its toolchain decodes exactly as before.
func TestDecodeToolsAbsent(t *testing.T) {
	m, err := Decode(mapObj{"name": "go"})
	require.NoError(t, err)
	assert.Empty(t, m.Tools)
}

// Everything magus knows about one binary arrives in one entry, keyed by the bin an op
// already names - which is what replaced five parallel declarations.
func TestDecodeToolsCarriesProbeKeyAndReady(t *testing.T) {
	m, err := Decode(mapObj{
		"name": "docker",
		"tools": map[string]any{
			"docker": map[string]any{
				"probe": map[string]any{"bin": "docker", "args": []string{"--version"}},
				"key":   map[string]any{"upTo": "patch"},
				"ready": map[string]any{"bin": "docker", "args": []string{"info"}},
			},
			"hadolint": map[string]any{
				"probe": map[string]any{"bin": "hadolint", "args": []string{"--version"}},
			},
		},
	})
	require.NoError(t, err)

	d := m.Tools["docker"]
	assert.Equal(t, "docker", d.Probe.Bin)
	assert.Equal(t, spells.VersionPatch, d.Key.UpTo)
	assert.Equal(t, "info", d.Ready.Args[0])

	// The asymmetry that motivated per-tool scoping: hadolint is gated by nothing.
	h := m.Tools["hadolint"]
	assert.Equal(t, "hadolint", h.Probe.Bin)
	assert.Empty(t, h.Ready.Bin, "linting a Dockerfile must not wait on the docker daemon")
}

// A constant stands in for a tool that cannot report a version, and needs no probe.
func TestDecodeToolsConstNeedsNoProbe(t *testing.T) {
	m, err := Decode(mapObj{
		"name":  "x",
		"tools": map[string]any{"protoc-gen-go": map[string]any{"key": map[string]any{"const": "protoc-gen-go-1"}}},
	})
	require.NoError(t, err)
	tool := m.Tools["protoc-gen-go"]
	assert.Equal(t, "protoc-gen-go-1", tool.Key.Const)
	assert.True(t, tool.HasProbe(), "a constant is a version magus can key on")
}

// The whole point of validating at decode: a typo'd component would otherwise leave a
// spell claiming "major" while the cache silently kept keying on the whole output.
func TestDecodeToolsRejectsUnknownComponent(t *testing.T) {
	_, err := Decode(mapObj{
		"name":  "go",
		"tools": map[string]any{"go": map[string]any{"key": map[string]any{"upTo": "mayor"}}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `tools["go"].key.upTo`)
	assert.Contains(t, err.Error(), "mayor")
}

// An entry declaring nothing at all is dropped rather than kept as a tool magus knows
// nothing about.
func TestDecodeToolsDropsEmptyEntries(t *testing.T) {
	m, err := Decode(mapObj{"name": "go", "tools": map[string]any{"gofmt": map[string]any{}}})
	require.NoError(t, err)
	assert.Empty(t, m.Tools)
}

// Diagnostics is declared on the op, not the tool: only the invocation knows which
// binary is actually reporting when a bin is shared by a wrapper (pnpm exec eslint vs
// pnpm exec tsc).
func TestDecodeOpsCarriesDiagnosticsFormat(t *testing.T) {
	m, err := Decode(mapObj{
		"name": "docker",
		"ops": map[string]any{
			"hadolint": map[string]any{
				"bin":         "hadolint",
				"args":        []string{"-f", "gnu", "Dockerfile"},
				"diagnostics": "gnu",
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, spells.DiagnosticGNU, m.Ops["hadolint"].Diagnostics)
}

// An unrecognized format is a spell-authoring bug caught at decode, the same place a
// bad key.upTo lands - not a silent fall back to scraping prose.
func TestDecodeOpsRejectsUnknownDiagnosticsFormat(t *testing.T) {
	_, err := Decode(mapObj{
		"name": "docker",
		"ops": map[string]any{
			"hadolint": map[string]any{
				"bin":         "hadolint",
				"diagnostics": "sarif",
			},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "diagnostics is sarif")
	assert.Contains(t, err.Error(), "gnu", "the message names the formats magus accepts")
}

// A tsc-shaped op with a custom pattern decodes and validates the pattern up front,
// the same place a bad key.upTo or an unrecognized format lands.
func TestDecodeOpsCarriesCustomDiagnosticPattern(t *testing.T) {
	pattern := `^(?P<file>.+?)\((?P<line>\d+),(?P<col>\d+)\): (?P<severity>error|warning) (?P<code>TS\d+): (?P<message>.*)$`
	m, err := Decode(mapObj{
		"name": "typescript",
		"ops": map[string]any{
			"tsc": map[string]any{
				"bin":               "pnpm",
				"args":              []string{"exec", "tsc"},
				"diagnostics":       "custom",
				"diagnosticPattern": pattern,
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, spells.DiagnosticCustom, m.Ops["tsc"].Diagnostics)
	assert.Equal(t, pattern, m.Ops["tsc"].DiagnosticPattern)
}

// An uncompilable pattern is a spell-authoring bug caught before anything runs, not a
// silent zero-findings result the first time the op actually fails.
func TestDecodeOpsRejectsUncompilableDiagnosticPattern(t *testing.T) {
	_, err := Decode(mapObj{
		"name": "x",
		"ops": map[string]any{
			"tsc": map[string]any{"bin": "tsc", "diagnostics": "custom", "diagnosticPattern": "(unclosed"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not compile")
}

// A pattern missing the required "file" or "line" capture group is rejected: a finding
// with no file and no line isn't a finding - it's exactly what a CI annotation needs
// to point at.
func TestDecodeOpsRejectsDiagnosticPatternMissingRequiredGroups(t *testing.T) {
	_, err := Decode(mapObj{
		"name": "x",
		"ops": map[string]any{
			"tsc": map[string]any{"bin": "tsc", "diagnostics": "custom", "diagnosticPattern": `^(?P<message>.*)$`},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `must name capture groups "file" and "line"`)
}

// diagnostics defaults to none, so custom requires opting in explicitly - a pattern
// with no diagnostics=custom is a likely spell-authoring mistake (forgot to flip the
// format), not something to silently ignore.
func TestDecodeOpsRejectsDiagnosticPatternWithoutCustomFormat(t *testing.T) {
	_, err := Decode(mapObj{
		"name": "x",
		"ops": map[string]any{
			"tsc": map[string]any{"bin": "tsc", "diagnosticPattern": `^(?P<file>.+):(?P<line>\d+)$`},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "diagnosticPattern is set but diagnostics is unset, not custom")
}

// diagnostics=custom with no pattern at all is the same authoring mistake from the
// other direction.
func TestDecodeOpsRejectsCustomFormatWithoutPattern(t *testing.T) {
	_, err := Decode(mapObj{
		"name": "x",
		"ops":  map[string]any{"tsc": map[string]any{"bin": "tsc", "diagnostics": "custom"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "diagnostics is custom but diagnosticPattern is empty")
}
