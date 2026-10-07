package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/scipbuzz"
	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// TestVersion pins the output another tool parses to probe the installed indexer.
func TestVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, run(context.Background(), []string{"--version"}, &stdout, &stderr))
	require.Equal(t, "scip-buzz "+scipbuzz.Version+"\n", stdout.String())
	require.Empty(t, stderr.String())
	require.Equal(t, "0.1.0", scipbuzz.Version)
}

func TestWritesTheIndexOfTheWorkingDirectory(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(ws, "magus.yaml"), nil, 0o644))
	project := filepath.Join(ws, "app")
	require.NoError(t, os.MkdirAll(project, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(project, "main.buzz"), []byte("export fun run() > void {}\n"), 0o644))
	t.Chdir(project)

	var stdout, stderr bytes.Buffer
	require.Equal(t, 1, run(context.Background(), []string{"--output", "out/index.scip"}, &stdout, &stderr), "out/ does not exist yet")

	require.NoError(t, os.Mkdir("out", 0o755))
	stderr.Reset()
	require.Equal(t, 0, run(context.Background(), []string{"--output", "out/index.scip"}, &stdout, &stderr), stderr.String())
	data, err := os.ReadFile(filepath.Join(project, "out", "index.scip"))
	require.NoError(t, err)
	var idx scip.Index
	require.NoError(t, proto.Unmarshal(data, &idx))
	require.Len(t, idx.Documents, 1)
	require.Equal(t, "main.buzz", idx.Documents[0].RelativePath)
	require.Equal(t, "scip-buzz buzz . . `app/main.buzz`/run().", idx.Documents[0].Symbols[0].Symbol)
	require.Empty(t, idx.Metadata.ToolInfo.Arguments, "arguments carry checkout paths")
}

// TestIndexBytesDoNotDependOnTheCheckoutPath runs the command as magus does, with
// an absolute --workspace-root, from two checkouts of one tree.
func TestIndexBytesDoNotDependOnTheCheckoutPath(t *testing.T) {
	index := func(ws string) []byte {
		project := filepath.Join(ws, "app")
		require.NoError(t, os.MkdirAll(project, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(project, "main.buzz"), []byte("export fun run() > void {}\n"), 0o644))
		t.Chdir(project)
		var stdout, stderr bytes.Buffer
		require.Equal(t, 0, run(context.Background(), []string{"--workspace-root", ws, "--output", "index.scip"}, &stdout, &stderr), stderr.String())
		data, err := os.ReadFile(filepath.Join(project, "index.scip"))
		require.NoError(t, err)
		return data
	}
	require.Equal(t, index(t.TempDir()), index(filepath.Join(t.TempDir(), "another", "checkout")))
}

func TestDefaultsToIndexScipInTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.buzz"), []byte("final x = 1;\n"), 0o644))
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, run(context.Background(), []string{"--workspace-root", dir}, &stdout, &stderr), stderr.String())
	_, err := os.Stat(filepath.Join(dir, "index.scip"))
	require.NoError(t, err)
}
