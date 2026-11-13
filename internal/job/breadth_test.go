package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// breadthWorkspace is a root project holding pkg/ and a second project, web, each with Go
// files, so a glob can reach one project or both.
func breadthWorkspace(t *testing.T, extra ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range append([]string{
		"magusfile.buzz",
		"pkg/a.go",
		"pkg/b.go",
		"pkg/a_test.go",
		"web/magusfile.buzz",
		"web/x.go",
	}, extra...) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(rel), 0o644))
	}
	return root
}

func symbolsAt(defs map[string][]string) SymbolReader {
	return func(_ context.Context, name, _ string) (SymbolFact, bool) {
		files, ok := defs[name]
		return SymbolFact{DefinedIn: files}, ok
	}
}

func TestBroadClaimRefusesAGlobAcrossProjects(t *testing.T) {
	root := breadthWorkspace(t)
	row := types.Job{ID: "sweep", Paths: types.JobPaths{Write: []string{"pkg/a.go", "**/*.go"}}, Agent: agent.Instructed("Edit `pkg/b.go`.")}

	got := broadClaimProblems(context.Background(), root, row, Workspace{}, nil)

	assert.Equal(t, types.JobProblems{{
		Code:    types.WritePathTooBroad,
		Field:   "paths.write[1]",
		Subject: "**/*.go",
		Message: `write path "**/*.go" matches 4 files in 2 projects: more than 1 project, ` +
			"a leading `**`, which reaches every directory of the workspace",
		Fix: `replace "**/*.go" with the files the goals and criteria name: "paths": {"write": ["pkg/b.go"]}; ` +
			`or keep it with the deviation {"code":"MGS3043","subject":"**/*.go","reason":"<why this job needs all of it>"}`,
	}}, got)
}

func TestBroadClaimRefusesAGlobPastTheFileLimit(t *testing.T) {
	var many []string
	for i := range config.Defaults().Jobs.MaxClaimFiles + 1 {
		many = append(many, fmt.Sprintf("pkg/gen/f%02d.txt", i))
	}
	root := breadthWorkspace(t, many...)
	row := types.Job{ID: "gen", Paths: types.JobPaths{Write: []string{"pkg/gen/*.txt"}}}

	got := broadClaimProblems(context.Background(), root, row, Workspace{}, nil)

	require.Len(t, got, 1)
	assert.Equal(t, `write path "pkg/gen/*.txt" matches 51 files in 1 project: more than 50 files`, got[0].Message)
	assert.Contains(t, got[0].Fix, "the criteria and goals name no file the graph resolves")
}

func TestBroadClaimReadsTheWorkspaceBounds(t *testing.T) {
	ctx := context.Background()
	across := types.Job{ID: "both", Paths: types.JobPaths{Write: []string{"*/*.go"}}}
	ws := Workspace{Projects: []*types.Project{{Path: "."}, {Path: "web"}}}
	inPkg := types.Job{ID: "pkg", Paths: types.JobPaths{Write: []string{"pkg/*.go"}}}

	t.Run("max_claim_projects raised admits two projects", func(t *testing.T) {
		root := breadthWorkspace(t)
		require.NoError(t, os.WriteFile(filepath.Join(root, config.Filename), []byte("jobs:\n  max_claim_projects: 2\n"), 0o644))
		assert.Empty(t, broadClaimProblems(ctx, root, across, ws, nil))
	})

	t.Run("max_claim_files lowered refuses a small glob", func(t *testing.T) {
		root := breadthWorkspace(t)
		require.NoError(t, os.WriteFile(filepath.Join(root, config.Filename), []byte("jobs:\n  max_claim_files: 2\n"), 0o644))
		got := broadClaimProblems(ctx, root, inPkg, Workspace{}, nil)
		require.Len(t, got, 1)
		assert.Equal(t, `write path "pkg/*.go" matches 3 files in 1 project: more than 2 files`, got[0].Message)
	})

	t.Run("a written zero is refused, never read as no bound", func(t *testing.T) {
		root := breadthWorkspace(t)
		require.NoError(t, os.WriteFile(filepath.Join(root, config.Filename), []byte("jobs:\n  max_claim_files: 0\n"), 0o644))
		got := broadClaimProblems(ctx, root, inPkg, Workspace{}, nil)
		require.Len(t, got, 1)
		assert.Equal(t, types.WritePathTooBroad, got[0].Code)
		assert.Contains(t, got[0].Message, "max_claim_files")
	})
}

func TestBroadClaimPassesAGlobInOneProject(t *testing.T) {
	root := breadthWorkspace(t)
	row := types.Job{ID: "pkg", Paths: types.JobPaths{Write: []string{"pkg/*.go"}}}

	assert.Empty(t, broadClaimProblems(context.Background(), root, row, Workspace{}, nil))
}

func TestBroadClaimNeverMeasuresAClaimALiteralOrAReadOnlyRow(t *testing.T) {
	root := breadthWorkspace(t)
	ctx := context.Background()

	assert.Empty(t, broadClaimProblems(ctx, root, types.Job{ID: "claim", Paths: types.JobPaths{Write: []string{"pkg/a.go#Foo", "web/x.go"}}}, Workspace{}, nil))
	assert.Empty(t, broadClaimProblems(ctx, root, types.Job{ID: "scout", ReadOnly: true, Paths: types.JobPaths{Write: []string{"**/*.go"}}}, Workspace{}, nil))
}

type broadClaimIntegrationSuite struct {
	suite.Suite
}

func TestBroadClaimIntegration(t *testing.T) { suite.Run(t, new(broadClaimIntegrationSuite)) }

func (s *broadClaimIntegrationSuite) TestSkipsAnIntegrationRow() {
	root := breadthWorkspace(s.T())
	row := types.Job{ID: "merge", Integrates: []string{"a"}, Paths: types.JobPaths{Write: []string{"**/*.go"}, Soft: []string{"**/*.go"}}, }

	s.Empty(broadClaimProblems(s.T().Context(), root, row, Workspace{}, nil))
}

func (s *broadClaimIntegrationSuite) TestStillRefusesTheSameGlobWithoutIntegrates() {
	root := breadthWorkspace(s.T())
	row := types.Job{ID: "sweep", Paths: types.JobPaths{Write: []string{"**/*.go"}, Soft: []string{"**/*.go"}}, }

	got := broadClaimProblems(s.T().Context(), root, row, Workspace{}, nil)

	s.Require().Len(got, 1)
	s.Equal(types.WritePathTooBroad, got[0].Code)
}

func TestBroadClaimDropsIgnoredFilesAndReadsTheLoadedProjects(t *testing.T) {
	root := breadthWorkspace(t)
	row := types.Job{ID: "both", Paths: types.JobPaths{Write: []string{"*/*.go"}}}
	ws := Workspace{Projects: []*types.Project{{Path: "."}, {Path: "web"}}}

	got := broadClaimProblems(context.Background(), root, row, ws, nil)
	require.Len(t, got, 1)
	assert.Equal(t, `write path "*/*.go" matches 4 files in 2 projects: more than 1 project`, got[0].Message)

	ws.Ignored = func(files []string) []string {
		return slices.DeleteFunc(slices.Clone(files), func(f string) bool { return f != "web/x.go" })
	}
	assert.Empty(t, broadClaimProblems(context.Background(), root, row, ws, nil))
}

func TestBroadClaimPermittedPathPassesThroughPermit(t *testing.T) {
	root := breadthWorkspace(t)
	row := types.Job{ID: "sweep", Paths: types.JobPaths{Write: []string{"**/*.go"}}}

	kept, permitted := broadClaimProblems(context.Background(), root, row, Workspace{}, nil).
		Permit([]types.Deviation{{Code: types.WritePathTooBroad, Subject: "**/*.go", Reason: "a rename touches every package"}})

	assert.Empty(t, kept)
	require.Len(t, permitted, 1)
	assert.Equal(t, "**/*.go", permitted[0].Subject)
}

func TestBroadClaimFixSaysTheGraphDidNotAnswer(t *testing.T) {
	root := breadthWorkspace(t)
	row := types.Job{
		ID:         "sweep",
		Paths: types.JobPaths{Write: []string{"**/*.go"}},
		Agent:      agent.Instructed("Fill (web/x.go:12)."),
		Goals:      []types.Goal{{ID: "funcs", Kind: types.GoalKindSymbol, Symbols: []string{"Missing"}}},
	}

	for name, read := range map[string]SymbolReader{"no reader": nil, "unanswered": symbolsAt(nil)} {
		t.Run(name, func(t *testing.T) {
			got := broadClaimProblems(context.Background(), root, row, Workspace{}, read)
			require.Len(t, got, 1)
			assert.Contains(t, got[0].Fix, `"paths": {"write": ["web/x.go"]} (the knowledge graph did not answer the symbol goals`)
		})
	}
}

func TestNarrowestCoverNamesTheDefiningFileItsTestsAndTheCriteriaFiles(t *testing.T) {
	root := breadthWorkspace(t)
	row := types.Job{
		ID:    "fill",
		Agent: agent.Instructed("Implement Foo; see `web/x.go`, the claim pkg/b.go#Bar, and docs/missing.md, a/b and v1.2."),
		Goals: []types.Goal{{ID: "funcs", Kind: types.GoalKindSymbol, Symbols: []string{"Foo"}}},
	}

	got := narrowestCover(context.Background(), root, row, symbolsAt(map[string][]string{"Foo": {"pkg/a.go"}}))

	assert.Equal(t, []string{"pkg/a.go", "pkg/*_test.go", "web/x.go", "pkg/b.go#Bar"}, got)
}
