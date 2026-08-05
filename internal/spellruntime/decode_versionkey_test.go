package spellruntime

import (
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A spell declaring no version_key keeps the conservative default, so every existing
// spell decodes exactly as before.
func TestDecodeVersionKeyAbsentIsZero(t *testing.T) {
	m, err := Decode(mapObj{"name": "go"})
	require.NoError(t, err)
	assert.True(t, m.VersionKey.IsZero())
	assert.Empty(t, m.VersionKeys)
}

func TestDecodeVersionKeyPrimaryAndNamed(t *testing.T) {
	m, err := Decode(mapObj{
		"name":        "go",
		"version_key": map[string]any{"upTo": "minor"},
		"version_keys": map[string]any{
			"golangci-lint": map[string]any{"upTo": "major"},
			"protoc-gen-go": map[string]any{"const": "protoc-gen-go-1"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, spells.VersionMinor, m.VersionKey.UpTo)
	assert.Equal(t, spells.VersionMajor, m.VersionKeys["golangci-lint"].UpTo)
	assert.Equal(t, "protoc-gen-go-1", m.VersionKeys["protoc-gen-go"].Const)
}

// The whole point of validating at decode: a typo'd component would otherwise leave a
// spell claiming "major" while the cache silently kept keying exactly, which looks
// like a cache that simply never hits and names nothing.
func TestDecodeVersionKeyRejectsUnknownComponent(t *testing.T) {
	_, err := Decode(mapObj{"name": "go", "version_key": map[string]any{"upTo": "mayor"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version_key.upTo")
	assert.Contains(t, err.Error(), "mayor")

	_, err = Decode(mapObj{
		"name":         "go",
		"version_keys": map[string]any{"golangci-lint": map[string]any{"upTo": "MAJOR"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `version_keys["golangci-lint"]`)
}

// A zero-valued named entry asks for the default; carrying it would imply the spell
// said something about that tool when it did not.
func TestDecodeVersionKeyDropsEmptyNamedEntries(t *testing.T) {
	m, err := Decode(mapObj{
		"name":         "go",
		"version_keys": map[string]any{"gofmt": map[string]any{}},
	})
	require.NoError(t, err)
	assert.Empty(t, m.VersionKeys)
}
