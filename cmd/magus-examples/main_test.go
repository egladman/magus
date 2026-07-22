package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInject replaces only the content between a slug's markers, is idempotent, and
// errors loudly when a marker is missing (docs and examples must stay in lockstep).
func TestInject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	original := "intro\n\n<!-- example:one -->\nOLD\n<!-- /example -->\n\noutro\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))

	snip := map[string]string{"one": "```\nNEW\n```\n"}
	require.NoError(t, inject(path, snip))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "intro\n\n<!-- example:one -->\n```\nNEW\n```\n<!-- /example -->\n\noutro\n", string(got))
	assert.Contains(t, string(got), "intro", "prose outside markers is preserved")
	assert.Contains(t, string(got), "outro")

	// Idempotent: re-injecting the same snippet is a no-op.
	before := string(got)
	require.NoError(t, inject(path, snip))
	after, _ := os.ReadFile(path)
	assert.Equal(t, before, string(after), "re-injection is stable")

	// A missing marker is a hard error, not a silent skip.
	assert.Error(t, inject(path, map[string]string{"missing": "x"}))
}

// TestDocsHaveExampleMarkers: every example the generator produces has a marker pair
// in the docs, so `content-generate` can never render an example with nowhere to land.
func TestDocsHaveExampleMarkers(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "concepts", "knowledge.md"))
	require.NoError(t, err)
	doc := string(raw)
	for _, ex := range examples {
		assert.Contains(t, doc, "<!-- example:"+ex.slug+" -->", "docs/concepts/knowledge.md needs an open marker for %q", ex.slug)
		assert.Contains(t, doc, ex.command(), "docs should mention the command %q near its example", ex.command())
	}
	assert.Contains(t, doc, "<!-- /example -->")
}
