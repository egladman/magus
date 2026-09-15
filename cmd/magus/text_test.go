package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTextPresenceCountsOccurrencesAndFiles is the fact refs reports beside a symbol
// miss: a name that is no symbol anywhere can still be in the tree many times, and
// saying `absent` without this states something false about the workspace.
func TestTextPresenceCountsOccurrencesAndFiles(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.WriteTree(map[string]string{
		"a.go":       "const Ref = \"NEEDLE_VALUE\"\n",
		"docs/b.md":  "export NEEDLE_VALUE=1\nand again NEEDLE_VALUE\n",
		"c/d/e.json": "{\"k\": \"nothing here\"}\n",
	})

	hits, files, searched, skipped, err := textPresence(w.Root(), "NEEDLE_VALUE")
	require.NoError(t, err)
	assert.Equal(t, 3, hits, "two in the markdown, one in the go file")
	assert.Equal(t, 2, files)
	assert.Equal(t, 3, searched, "every file is read; the miss is a file with no match")
	assert.Zero(t, skipped)
}

// TestSearchableFilesSkipsMachineWrittenTrees pins the exclusions. They are named rather
// than inferred, so a wrong one here silently shrinks every answer.
func TestSearchableFilesSkipsMachineWrittenTrees(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.WriteTree(map[string]string{
		"kept.go":                   "package a",
		".git/objects/pack/x":       "binary-ish",
		".magus/outputs/y.out":      "captured log",
		"node_modules/pkg/index.js": "module.exports = {}",
	})

	paths, _, err := searchableFiles(w.Root())
	require.NoError(t, err)

	rels := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, relErr := filepath.Rel(w.Root(), p)
		require.NoError(t, relErr)
		rels = append(rels, rel)
	}
	assert.Equal(t, []string{"kept.go"}, rels)
}

// TestSearchableFilesCountsWhatItDeclined is the property that keeps a short answer
// distinguishable from a small one. A count that hides its exclusions is the failure
// mode that sends a reader back to grep permanently.
func TestSearchableFilesCountsWhatItDeclined(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("kept.go", "package a")
	w.Write("empty.txt", "")
	big := make([]byte, maxSearchableFile+1)
	for i := range big {
		big[i] = 'x'
	}
	require.NoError(t, os.WriteFile(w.Path("huge.txt"), big, 0o644))

	paths, skipped, err := searchableFiles(w.Root())
	require.NoError(t, err)
	assert.Len(t, paths, 1)
	assert.Equal(t, 2, skipped, "the empty file and the oversized one are both declined, and both counted")
}

// TestTextPresenceSkipsBinary covers the NUL-byte heuristic: a match inside a binary
// yields a line nobody can read and a column nobody can act on.
func TestTextPresenceSkipsBinary(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("text.txt", "NEEDLE here\n")
	require.NoError(t, os.WriteFile(w.Path("blob.bin"), []byte("NEEDLE\x00padding"), 0o644))

	hits, files, _, _, err := textPresence(w.Root(), "NEEDLE")
	require.NoError(t, err)
	assert.Equal(t, 1, hits)
	assert.Equal(t, 1, files, "the binary holds the bytes but is not a searchable file")
}
