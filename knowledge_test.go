package magus

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkGlobalWS writes a single-project workspace with one target and returns its root.
func mkGlobalWS(t *testing.T, target string) string {
	t.Helper()
	root := t.TempDir()
	src := "export fun " + target + "(args: [str]) > void {}\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(src), 0o644))
	return root
}

func TestBuildGlobalKnowledgeGraph(t *testing.T) {
	ctx := context.Background()
	rootA := mkGlobalWS(t, "build")
	rootB := mkGlobalWS(t, "deploy")

	wsA, err := Inspect(ctx, rootA)
	require.NoError(t, err)

	g, err := BuildGlobalKnowledgeGraph(ctx, wsA, config.Config{Knowledge: config.Knowledge{Workspaces: []string{rootB}}}, false, slog.Default())
	require.NoError(t, err)
	out := g.Output()

	nameA, nameB := filepath.Base(rootA), filepath.Base(rootB)
	var haveA, haveB bool
	for _, n := range out.Nodes {
		if strings.HasPrefix(n.ID, nameA+"//") {
			haveA = true
		}
		if strings.HasPrefix(n.ID, nameB+"//") {
			haveB = true
		}
	}
	assert.True(t, haveA, "current workspace nodes present, namespaced by %q", nameA)
	assert.True(t, haveB, "registered workspace nodes present, namespaced by %q", nameB)
	// Each workspace declared a distinct target; both should resolve in the union.
	assert.NotEmpty(t, g.Resolve("build", 1), "build target resolves in the global graph")
	assert.NotEmpty(t, g.Resolve("deploy", 1), "deploy target resolves in the global graph")
}

func TestBuildGlobalKnowledgeGraphSkipsUnreachable(t *testing.T) {
	ctx := context.Background()
	rootA := mkGlobalWS(t, "build")
	wsA, err := Inspect(ctx, rootA)
	require.NoError(t, err)

	// A registered workspace that does not exist is skipped, not fatal: the global
	// graph degrades to what it can reach rather than failing the whole query.
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	g, err := BuildGlobalKnowledgeGraph(ctx, wsA, config.Config{Knowledge: config.Knowledge{Workspaces: []string{missing}}}, false, slog.Default())
	require.NoError(t, err)
	assert.Positive(t, g.Output().NodeCount, "current workspace still present despite the missing registered one")
}
