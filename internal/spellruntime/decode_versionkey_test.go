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

// A tool declaring only a diagnostics format is kept: the entry says something magus
// acts on, so the drop-empty-entries rule must not treat it as saying nothing.
func TestDecodeToolsCarriesDiagnosticsFormat(t *testing.T) {
	m, err := Decode(mapObj{
		"name": "docker",
		"tools": map[string]any{
			"hadolint": map[string]any{"diagnostics": "gnu"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, spells.DiagnosticGNU, m.Tools["hadolint"].Diagnostics)
}

// An unrecognized format is a spell-authoring bug caught at decode, the same place a
// bad key.upTo lands - not a silent fall back to scraping prose.
func TestDecodeToolsRejectsUnknownDiagnosticsFormat(t *testing.T) {
	_, err := Decode(mapObj{
		"name": "docker",
		"tools": map[string]any{
			"hadolint": map[string]any{"diagnostics": "sarif"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "diagnostics is sarif")
	assert.Contains(t, err.Error(), "gnu", "the message names the formats magus accepts")
}
