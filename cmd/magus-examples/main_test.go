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

// TestReviewFixtureHasNoIndexer: a spell in the review fixture gives its project a symbol
// indexer, and `magus diff` then runs it, so the captured page would depend on whether that
// indexer is installed on the machine that regenerated it.
func TestReviewFixtureHasNoIndexer(t *testing.T) {
	assert.NotContains(t, fixtures[reviewFixture]["magusfile.buzz"], "magus/spell/")
	for _, ex := range examples {
		assert.Contains(t, fixtures, ex.fixture, "example %q names an unknown fixture", ex.slug)
	}
}
