//go:build !wasm

package std

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommentsBlocksReadsDeclaredExtensions(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\n// Alpha.\n//\n//\tcode()\nfunc f() {} // trailing\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.sh"), []byte("# not read\n"), 0o644))
	syntax := map[string]any{".go": map[string]any{
		"lineComments":  []any{"//"},
		"blockComments": []any{map[string]any{"open": "/*", "close": "*/"}},
		"quotes":        []any{map[string]any{"open": "\"", "close": "\""}},
		"directives":    []any{"go:"},
	}}

	got, err := CommentsBlocks(WithCwd(t.Context(), dir), []string{"a.go", "b.sh"}, syntax)
	require.NoError(t, err)
	assert.Equal(t, []any{
		map[string]any{"path": "a.go", "lines": []any{map[string]any{"line": 3, "col": 4, "text": "Alpha."}}},
		map[string]any{"path": "a.go", "lines": []any{map[string]any{"line": 6, "col": 16, "text": "trailing"}}},
	}, got, "paths keep the spelling the caller gave; an undeclared extension is skipped")
}

func TestCommentsBlocksRefusesMalformedSyntax(t *testing.T) {
	_, err := CommentsBlocks(t.Context(), nil, map[string]any{".go": "not a syntax"})
	require.ErrorContains(t, err, "comments.blocks: syntax")
}
