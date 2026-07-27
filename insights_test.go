package magus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInsightsUseHistoryAndProjectLabels(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	commitInsightFiles(t, root, "Ada", "2026-01-01T00:00:00Z", map[string]string{
		"magusfile.buzz":      "# root\n",
		"api/magusfile.buzz":  "# api\n",
		"api/service.go":      "package api\n",
		"docs/magusfile.buzz": "# docs\n",
		"docs/guide.md":       "# guide\n",
	})
	commitInsightFiles(t, root, "Ada", "2026-01-02T00:00:00Z", map[string]string{"api/service.go": "package api\n// second\n"})
	commitInsightFiles(t, root, "Lin", "2026-01-03T00:00:00Z", map[string]string{"api/service.go": "package api\n// third\n"})
	commitInsightFiles(t, root, "Lin", "2026-01-04T00:00:00Z", map[string]string{"docs/guide.md": "# updated\n"})

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".")
	reg.RegisterProject("api", WithDependsOn(".."))
	reg.RegisterProject("docs", WithDependsOn("api"))
	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)
	m := ws.(*Magus)
	canonicalRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	rootName := filepath.Base(canonicalRoot)
	opts := types.InsightOptions{Commits: 10}

	affinity, err := m.Affinity(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, types.AffinityOutput{
		Definition: types.AffinityDefinition,
		Commits:    10,
		Pairs: []types.CoChange{
			{A: ".", AName: rootName, B: "api", BName: "api", Count: 1},
			{A: ".", AName: rootName, B: "docs", BName: "docs", Count: 1, Hidden: true},
			{A: "api", AName: "api", B: "docs", BName: "docs", Count: 1},
		},
	}, affinity)

	ownership, err := m.Ownership(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, types.OwnershipOutput{
		Definition: types.OwnershipDefinition,
		Commits:    10,
		Projects: []types.Ownership{
			{Path: ".", Name: rootName, Commits: 1, Authors: 1, Primary: "Ada", PrimaryShare: 100, BusFactor1: true, Stale: true, LastCommit: insightTime(1)},
			{Path: "api", Name: "api", Commits: 3, Authors: 2, Primary: "Ada", PrimaryShare: 66, LastCommit: insightTime(3)},
			{Path: "docs", Name: "docs", Commits: 2, Authors: 2, Primary: "Ada", PrimaryShare: 50, LastCommit: insightTime(4)},
		},
	}, ownership)

	trend, err := m.Trend(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, types.TrendOutput{
		Definition: types.TrendDefinition,
		Commits:    10,
		Projects: []types.Trend{
			{Path: "docs", Name: "docs", Recent: 1, Earlier: 1, Delta: 0},
			{Path: ".", Name: rootName, Earlier: 1, Delta: -1},
			{Path: "api", Name: "api", Recent: 1, Earlier: 2, Delta: -1},
		},
	}, trend)

	hotspots, err := m.Hotspots(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, types.HotspotOutput{
		Definition: types.HotspotDefinition,
		Commits:    10,
		Nodes: []types.Node{
			{Path: ".", Name: rootName, Children: []string{}, Dir: canonicalRoot, BlastRadius: 3, Churn: 1, Authors: 1, LastCommit: insightTimePtr(1)},
			{Path: "api", Name: "api", Children: []string{"."}, Dir: filepath.Join(canonicalRoot, "api"), BlastRadius: 2, Churn: 3, Authors: 2, LastCommit: insightTimePtr(3)},
			{Path: "docs", Name: "docs", Children: []string{"api"}, Dir: filepath.Join(canonicalRoot, "docs"), BlastRadius: 1, Churn: 2, Authors: 2, LastCommit: insightTimePtr(4)},
		},
	}, hotspots)
}

func commitInsightFiles(t *testing.T, dir, author, when string, files map[string]string) {
	t.Helper()
	for path, body := range files {
		full := filepath.Join(dir, path)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}
	gitRun(t, dir, "add", "-A")
	cmd := exec.Command("git", "-C", dir, "commit", "-q", "-m", "insight")
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+author,
		"GIT_AUTHOR_EMAIL="+strings.ToLower(author)+"@example.test",
		"GIT_AUTHOR_DATE="+when,
		"GIT_COMMITTER_NAME="+author,
		"GIT_COMMITTER_EMAIL="+strings.ToLower(author)+"@example.test",
		"GIT_COMMITTER_DATE="+when,
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git commit: %s", out)
}

func insightTime(day int) time.Time {
	return time.Date(2026, time.January, day, 0, 0, 0, 0, time.UTC)
}

func insightTimePtr(day int) *time.Time {
	t := insightTime(day)
	return &t
}
