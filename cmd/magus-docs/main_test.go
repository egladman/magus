package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/docs"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/std"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestPruneRemovesOnlyStaleModulePages(t *testing.T) {
	out := t.TempDir()
	modules := []std.Module{{Name: "keep"}}
	require.NoError(t, os.WriteFile(filepath.Join(out, "keep.md"), []byte(renderModule(modules[0])), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(out, "index.md"), []byte(renderIndex(modules)), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(out, "keep 3.md"), []byte(renderModule(modules[0])), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(out, "index 3.md"), []byte(renderIndex(modules)), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(out, "authored.md"), []byte("---\ngenerated_from: elsewhere.md\n---\n# Keep me\n"), 0o644))

	require.NoError(t, prune(out, modules))

	assert.FileExists(t, filepath.Join(out, "keep.md"))
	assert.FileExists(t, filepath.Join(out, "index.md"))
	assert.NoFileExists(t, filepath.Join(out, "keep 3.md"))
	assert.NoFileExists(t, filepath.Join(out, "index 3.md"))
	assert.FileExists(t, filepath.Join(out, "authored.md"))
}

func TestSourceLinksNameTheFileNotTheLine(t *testing.T) {
	assert.Equal(t, "[`Callback`]("+docs.RepoBlob+"/std/module.go)", typeMarkdown(std.TypeFunc))

	file := std.MethodSource(std.Method{Impl: std.FsGlob}, repoRoot)
	if file == "" {
		t.Skip("built with -trimpath: no absolute path to relativize")
	}
	assert.Equal(t, " - [source]("+docs.RepoBlob+"/"+file+")", methodSourceLink(std.Method{Impl: std.FsGlob}))
}
