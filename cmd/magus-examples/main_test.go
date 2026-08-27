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
	// A blank line on both sides of the fence. Not cosmetic: dprint's markdown formatter wants
	// them there, so without them the formatter rewrites what this just wrote and the two undo
	// each other on every run.
	assert.Equal(t, "intro\n\n<!-- example:one -->\n\n```\nNEW\n```\n\n<!-- /example -->\n\noutro\n", string(got))
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

// TestDocsHaveExampleMarkers: every example the generator produces has a marker pair on
// the page it names, so `content-generate` can never render an example with nowhere to
// land. Each example is checked against ITS OWN page rather than one shared file - an
// example whose markers live on a different page than it declares is exactly the failure
// that would otherwise surface as a generate error in CI.
func TestDocsHaveExampleMarkers(t *testing.T) {
	pages := map[string]string{}
	for _, ex := range examples {
		doc, ok := pages[ex.docs]
		if !ok {
			raw, err := os.ReadFile(filepath.Join("..", "..", "docs", ex.docs))
			require.NoError(t, err, "example %q names a page that does not exist", ex.slug)
			doc = string(raw)
			pages[ex.docs] = doc
		}
		assert.Contains(t, doc, "<!-- example:"+ex.slug+" -->", "docs/%s needs an open marker for %q", ex.docs, ex.slug)
		assert.Contains(t, doc, "<!-- /example -->", "docs/%s needs a closing marker", ex.docs)
		assert.Contains(t, doc, ex.command(), "docs/%s should mention the command %q near its example", ex.docs, ex.command())
	}
}
