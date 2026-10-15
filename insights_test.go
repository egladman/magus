package magus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInsightsUseHistoryAndProjectLabels(t *testing.T) {
	// A workspace named "magus" so the root display reads as "magus" rather
	// than as the temp dir's auto-generated basename.
	root := filepath.Join(t.TempDir(), "magus")
	require.NoError(t, os.MkdirAll(root, 0o755))
	gitRun(t, root, "init", "-q")
	commitInsightFiles(t, root, "Ada", "2026-01-01T00:00:00Z", map[string]string{
		"magusfile.buzz":      "// root\n",
		"api/magusfile.buzz":  "// api\n",
		"api/service.go":      "package api\n",
		"docs/magusfile.buzz": "// docs\n",
		"docs/guide.md":       "# guide\n",
	})
	commitInsightFiles(t, root, "Ada", "2026-01-02T00:00:00Z", map[string]string{"api/service.go": "package api\n// second\n"})
	commitInsightFiles(t, root, "Lin", "2026-01-03T00:00:00Z", map[string]string{"api/service.go": "package api\n// third\n"})
	commitInsightFiles(t, root, "Lin", "2026-01-04T00:00:00Z", map[string]string{"docs/guide.md": "# updated\n"})

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".")
	reg.RegisterProject("api", WithDependsOn(".."))
	reg.RegisterProject("docs", WithDependsOn("api"))
	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg), WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	m := ws.(*Magus)
	canonicalRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	rootName := "magus"
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
		Projects: []types.OwnershipEntry{
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
		Projects: []types.TrendEntry{
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

// gitRun is not a testkit.Repo: the commits here need their own author and committer
// identity and dates, and the workspace directory is named for what the test asserts.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = insightGitEnv()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
}

// insightGitEnv is the test's environment with the user's global and system git config
// switched off, so a signing key or a hooks path there cannot reach the fixture.
func insightGitEnv() []string {
	return append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
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
	cmd.Env = append(insightGitEnv(),
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

// TestDuplicationHistoryCountsFilesChangingTogether pins the co-change evidence: copies in
// separate files that always change together are drifting when one is edited alone, and
// copies that share one file carry no evidence at all.
func TestDuplicationHistoryCountsFilesChangingTogether(t *testing.T) {
	group := types.DuplicationGroup{Members: []types.DuplicationSite{{Source: "a.go:1"}, {Source: "b.go:9"}}}
	scan := []project.ScannedCommit{
		{Files: []string{"a.go", "b.go"}},
		{Files: []string{"a.go", "c.go"}},
		{Files: []string{"c.go"}},
	}
	assert.Equal(t, &types.DuplicationHistory{Commits: 3, Together: 1, Apart: 1}, duplicationHistory(group, scan))

	sameFile := types.DuplicationGroup{Members: []types.DuplicationSite{{Source: "a.go:1"}, {Source: "a.go:40"}}}
	assert.Nil(t, duplicationHistory(sameFile, scan))
}

// TestConformLensWithoutAnIndexIsUnknownAndMisconfigurationIsAnError pins both ends of the
// lens: a workspace with no symbol index answers unknown with the lens's definition, never an
// empty pass, and a ratchet record the configuration names but the tree lacks is an error
// that names it, never a lens that quietly falls back to the anchors.
func TestConformLensWithoutAnIndexIsUnknownAndMisconfigurationIsAnError(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("// root\n"), 0o644))
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".")

	open := func(cfg config.Config) *Magus {
		ws, err := Inspect(t.Context(), root, WithWorkspaceRegistry(reg), workspace.WithLoadedConfig(cfg), WithSpawnRule(TierCeiling))
		require.NoError(t, err)
		return ws.(*Magus)
	}

	out, err := open(config.Defaults()).Conform(t.Context())
	require.NoError(t, err)
	assert.Equal(t, types.ConformDefinition, out.Definition)
	wantAnswer := out.Answer
	wantAnswer.Verdict, wantAnswer.Reason = types.VerdictUnknown, types.ReasonSymbolIndexMissing
	assert.Equal(t, wantAnswer, out.Answer, "no index means unknown, never a pass")

	cfg := config.Defaults()
	cfg.Knowledge.Conform.Ratchet = "hack/policy/conformance.jsonl"
	_, err = open(cfg).Conform(t.Context())
	assert.ErrorContains(t, err, "hack/policy/conformance.jsonl")
}

// TestModelTierLensReportsWhatTheJobHistoryHolds pins the lens against the history file the
// job store writes: no file is an empty lens that still carries its definition, and a
// recorded outcome is one row.
func TestModelTierLensReportsWhatTheJobHistoryHolds(t *testing.T) {
	t.Parallel()
	m := &Magus{}
	m.cfg.HistoryPath = filepath.Join(t.TempDir(), "history.json")

	empty, err := m.ModelTierLens(t.Context())
	require.NoError(t, err, "a workspace that never ran a job has nothing to report, not a failure")
	assert.Equal(t, types.ModelTierOutput{Definition: types.ModelTiersDefinition, Rows: []types.ModelTierRecord{}}, empty)

	require.NoError(t, forecast.UpdateHistory(t.Context(), m.cfg.HistoryPath, func(h *forecast.History) error {
		h.RecordJob(types.JobOutcome{Job: "a", ModelTier: "economy", Kind: types.JobKindCheck, State: types.StatePass, ChecksRun: 2})
		return nil
	}))
	got, err := m.ModelTierLens(t.Context())
	require.NoError(t, err)
	require.Len(t, got.Rows, 1)
	wantRow := got.Rows[0]
	wantRow.ModelTier, wantRow.Rank, wantRow.ChecksPerJob = "economy", -1, 2
	assert.Equal(t, wantRow, got.Rows[0], "a workspace with no declaration ranks nothing")
}

// TestProjectDisplayName resolves the label the insight lenses attach to a scanned
// path. A path the workspace does not know still gets a label rather than an empty
// cell: a commit can touch a directory no project claims.
func TestProjectDisplayName(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
	})
	ws, err := Inspect(context.Background(), root, WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	m := ws.(*Magus)

	assert.Equal(t, "api", m.projectDisplayName("api"))
	assert.Equal(t, "deleted/project", m.projectDisplayName("deleted/project"),
		"a path no project claims still labels as itself")
	assert.NotEmpty(t, m.projectDisplayName("."), "the root project always has a label")
}

// TestDeclaredDeps is the lookup the affinity lens uses to decide whether a
// co-change pair is HIDDEN. It is indexed by project path, and both directions are
// consulted, so a project with no dependencies must still appear with an empty set
// rather than be absent.
func TestDeclaredDeps(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":     "",
		"lib/magusfile.buzz": "",
		"web/magusfile.buzz": "",
	})

	reg := NewWorkspaceRegistry()
	reg.RegisterProject("web", WithDependsOn("lib"))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg), WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	m := ws.(*Magus)

	deps := m.declaredDeps()
	require.Contains(t, deps, "web")
	require.Contains(t, deps, "lib")
	assert.True(t, deps["web"]["lib"], "a declared dependency is indexed in the direction it points")
	assert.Empty(t, deps["lib"], "a project with no dependencies is present with an empty set, not absent")
	assert.False(t, deps["lib"]["web"], "the edge is one-way")
}
