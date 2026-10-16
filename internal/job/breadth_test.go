package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	row := types.Job{ID: "sweep", WritePaths: []string{"pkg/a.go", "**/*.go"}, Criteria: "Edit `pkg/b.go`."}

	got := broadClaimProblems(context.Background(), root, row, Workspace{}, nil)

	require.Len(t, got, 1)
	assert.Equal(t, types.WritePathTooBroad, got[0].Code)
	assert.Equal(t, "write_paths[1]", got[0].Field)
	assert.Equal(t, "**/*.go", got[0].Subject)
	assert.Equal(t, `write path "**/*.go" matches 4 files in 2 projects: more than 1 project, `+
		"a leading `**`, which reaches every directory of the workspace", got[0].Message)
	assert.Equal(t, `replace "**/*.go" with the files the goals and criteria name: "write_paths": ["pkg/b.go"]; `+
		`or keep it with the waiver {"code":"MGS3043","subject":"**/*.go","reason":"<why this job needs all of it>"}`, got[0].Fix)
}

func TestBroadClaimRefusesAGlobPastTheFileLimit(t *testing.T) {
	var many []string
	for i := range maxClaimFiles + 1 {
		many = append(many, fmt.Sprintf("pkg/gen/f%02d.txt", i))
	}
	root := breadthWorkspace(t, many...)
	row := types.Job{ID: "gen", WritePaths: []string{"pkg/gen/*.txt"}}

	got := broadClaimProblems(context.Background(), root, row, Workspace{}, nil)

	require.Len(t, got, 1)
	assert.Equal(t, `write path "pkg/gen/*.txt" matches 51 files in 1 project: more than 50 files`, got[0].Message)
	assert.Contains(t, got[0].Fix, "the criteria and goals name no file the graph resolves")
}

func TestBroadClaimPassesAGlobInOneProject(t *testing.T) {
	root := breadthWorkspace(t)
	row := types.Job{ID: "pkg", WritePaths: []string{"pkg/*.go"}}

	assert.Empty(t, broadClaimProblems(context.Background(), root, row, Workspace{}, nil))
}

func TestBroadClaimNeverMeasuresAClaimALiteralOrAReadOnlyRow(t *testing.T) {
	root := breadthWorkspace(t)
	ctx := context.Background()

	assert.Empty(t, broadClaimProblems(ctx, root, types.Job{ID: "claim", WritePaths: []string{"pkg/a.go#Foo", "web/x.go"}}, Workspace{}, nil))
	assert.Empty(t, broadClaimProblems(ctx, root, types.Job{ID: "scout", ReadOnly: true, WritePaths: []string{"**/*.go"}}, Workspace{}, nil))
}

func TestBroadClaimDropsIgnoredFilesAndReadsTheLoadedProjects(t *testing.T) {
	root := breadthWorkspace(t)
	row := types.Job{ID: "both", WritePaths: []string{"*/*.go"}}
	ws := Workspace{Projects: []*types.Project{{Path: "."}, {Path: "web"}}}

	got := broadClaimProblems(context.Background(), root, row, ws, nil)
	require.Len(t, got, 1)
	assert.Equal(t, `write path "*/*.go" matches 4 files in 2 projects: more than 1 project`, got[0].Message)

	ws.Ignored = func(files []string) []string {
		return slices.DeleteFunc(slices.Clone(files), func(f string) bool { return f != "web/x.go" })
	}
	assert.Empty(t, broadClaimProblems(context.Background(), root, row, ws, nil))
}

func TestBroadClaimWaivedPathPassesThroughWaive(t *testing.T) {
	root := breadthWorkspace(t)
	row := types.Job{ID: "sweep", WritePaths: []string{"**/*.go"}}

	kept, waived := broadClaimProblems(context.Background(), root, row, Workspace{}, nil).
		Waive([]types.JobWaiver{{Code: types.WritePathTooBroad, Subject: "**/*.go", Reason: "a rename touches every package"}})

	assert.Empty(t, kept)
	require.Len(t, waived, 1)
	assert.Equal(t, "**/*.go", waived[0].Subject)
}

func TestBroadClaimFixSaysTheGraphDidNotAnswer(t *testing.T) {
	root := breadthWorkspace(t)
	row := types.Job{
		ID:         "sweep",
		WritePaths: []string{"**/*.go"},
		Criteria:   "Fill (web/x.go:12).",
		Goals:      []types.Goal{{ID: "funcs", Kind: types.GoalKindSymbol, Symbols: []string{"Missing"}}},
	}

	for name, read := range map[string]SymbolReader{"no reader": nil, "unanswered": symbolsAt(nil)} {
		t.Run(name, func(t *testing.T) {
			got := broadClaimProblems(context.Background(), root, row, Workspace{}, read)
			require.Len(t, got, 1)
			assert.Contains(t, got[0].Fix, `"write_paths": ["web/x.go"] (the knowledge graph did not answer the symbol goals`)
		})
	}
}

func TestNarrowestCoverNamesTheDefiningFileItsTestsAndTheCriteriaFiles(t *testing.T) {
	root := breadthWorkspace(t)
	row := types.Job{
		ID:       "fill",
		Criteria: "Implement Foo; see `web/x.go`, the claim pkg/b.go#Bar, and docs/missing.md, a/b and v1.2.",
		Goals:    []types.Goal{{ID: "funcs", Kind: types.GoalKindSymbol, Symbols: []string{"Foo"}}},
	}

	got := narrowestCover(context.Background(), root, row, symbolsAt(map[string][]string{"Foo": {"pkg/a.go"}}))

	assert.Equal(t, []string{"pkg/a.go", "pkg/*_test.go", "web/x.go", "pkg/b.go#Bar"}, got)
}
