//go:build mcp

package mcp

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParamString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "v", paramString(map[string]any{"k": "v"}, "k", "def"))
	assert.Equal(t, "def", paramString(map[string]any{"k": "v"}, "missing", "def"))
	assert.Equal(t, "def", paramString(map[string]any{"k": 42}, "k", "def")) // wrong type → default
	assert.Equal(t, "def", paramString(nil, "k", "def"))
}

func TestParamBool(t *testing.T) {
	t.Parallel()
	assert.True(t, paramBool(map[string]any{"dry_run": true}, "dry_run", false))
	assert.False(t, paramBool(map[string]any{"dry_run": false}, "dry_run", true))
	assert.False(t, paramBool(map[string]any{"dry_run": "yes"}, "dry_run", false)) // wrong type → default
	assert.True(t, paramBool(nil, "dry_run", true))
}

func TestParamFloat(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 3.14, paramFloat(map[string]any{"n": float64(3.14)}, "n", 0))
	assert.Equal(t, float64(7), paramFloat(map[string]any{"n": int(7)}, "n", 0))
	assert.Equal(t, float64(99), paramFloat(map[string]any{"n": int64(99)}, "n", 0))
	assert.Equal(t, 1.5, paramFloat(map[string]any{"n": "oops"}, "n", 1.5)) // wrong type → default
	assert.Equal(t, 2.0, paramFloat(nil, "n", 2.0))
}

func TestParseEventLines(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		assert.Empty(t, parseEventLines(bytes.NewBufferString("")))
	})
	t.Run("blank lines only", func(t *testing.T) {
		assert.Empty(t, parseEventLines(bytes.NewBufferString("\n\n")))
	})
	t.Run("single event", func(t *testing.T) {
		assert.Len(t, parseEventLines(bytes.NewBufferString(`{"type":"run"}`)), 1)
	})
	t.Run("two events", func(t *testing.T) {
		assert.Len(t, parseEventLines(bytes.NewBufferString("{\"type\":\"a\"}\n{\"type\":\"b\"}")), 2)
	})
	t.Run("whitespace around", func(t *testing.T) {
		assert.Len(t, parseEventLines(bytes.NewBufferString("  {\"k\":1}  \n")), 1)
	})
	t.Run("invalid json skipped", func(t *testing.T) {
		assert.Len(t, parseEventLines(bytes.NewBufferString("not-json\n{\"ok\":true}")), 1)
	})
}
