package symbols

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestFingerprintBodies(t *testing.T) {
	root := t.TempDir()
	src := "package a\n\nfunc F() {\n\treturn\n}\n\nconst C = 1"
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a", "a.go"), []byte(src), 0o644))
	lines := types.SplitSourceLines([]byte(src))
	body, _ := types.BodyDigest(lines, 3, 5)
	decl, _ := types.BodyDigest(lines, 7, 7)

	syms := []types.KnowledgeSymbol{
		{Key: "F", Source: "a/a.go:3", DefEndLine: 5},
		{Key: "C", Source: "a/a.go:7"},
		{Key: "Gone", Source: "a/gone.go:1"},
		{Key: "Past", Source: "a/a.go:6", DefEndLine: 99},
		{Key: "RefOnly"},
	}
	FingerprintBodies(root, syms)

	assert.Equal(t, []types.KnowledgeSymbol{
		{Key: "F", Source: "a/a.go:3", DefEndLine: 5, BodyDigest: body, SourceLines: 7, SourceBytes: len(src)},
		{Key: "C", Source: "a/a.go:7", BodyDigest: decl, SourceLines: 7, SourceBytes: len(src)},
		{Key: "Gone", Source: "a/gone.go:1"},
		// The file was read, so it is sized, but the range overruns it: no digest.
		{Key: "Past", Source: "a/a.go:6", DefEndLine: 99, SourceLines: 7, SourceBytes: len(src)},
		{Key: "RefOnly"},
	}, syms)
}
