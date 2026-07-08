package magus

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/internal/config"
)

func writeSCIP(t *testing.T, path string) {
	t.Helper()
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "pkg/a/a.go",
		Occurrences: []*scip.Occurrence{
			{Symbol: "scip-go gomod example.com/a v1 Foo#", SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{0, 0, 3}},
		},
	}}}
	data, err := proto.Marshal(idx)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))
}

func TestLoadKnowledgeSymbols(t *testing.T) {
	root := t.TempDir()
	writeSCIP(t, filepath.Join(root, "index.scip"))

	cfg := config.Config{Knowledge: config.Knowledge{Symbols: []config.SymbolIndex{
		{Project: "pkg/a", Index: "index.scip"},
	}}}
	got := loadKnowledgeSymbols(cfg, root, slog.Default())

	require.Len(t, got["pkg/a"], 1, "the declared index is read into symbols")
	assert.Equal(t, "gomod example.com/a Foo#", got["pkg/a"][0].Key)
}

func TestLoadKnowledgeSymbolsSkipsMissing(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Knowledge: config.Knowledge{Symbols: []config.SymbolIndex{
		{Project: "pkg/a", Index: "does-not-exist.scip"}, // target not run yet
	}}}
	got := loadKnowledgeSymbols(cfg, root, slog.Default())
	assert.Empty(t, got, "a missing index is skipped, not an error")
}

func TestLoadKnowledgeSymbolsNoneDeclared(t *testing.T) {
	got := loadKnowledgeSymbols(config.Config{}, t.TempDir(), slog.Default())
	assert.Nil(t, got)
}

func TestLoadKnowledgeSymbolsSkipsCorrupt(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "bad.scip"), []byte("not a protobuf"), 0o644))
	cfg := config.Config{Knowledge: config.Knowledge{Symbols: []config.SymbolIndex{
		{Project: "pkg/a", Index: "bad.scip"},
	}}}
	got := loadKnowledgeSymbols(cfg, root, slog.Default())
	assert.Empty(t, got, "an undecodable index is skipped, not fatal")
}

func TestLoadKnowledgeSymbolsRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{Knowledge: config.Knowledge{Symbols: []config.SymbolIndex{
		{Project: "pkg/a", Index: "../outside.scip"},
	}}}
	got := loadKnowledgeSymbols(cfg, root, slog.Default())
	assert.Empty(t, got, "an index path that escapes the workspace is rejected")
}
