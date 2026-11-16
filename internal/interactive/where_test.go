package interactive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"

	"github.com/egladman/magus/libs/testkit"
)

func TestWhereUniqueMatch(t *testing.T) {
	all := []*types.Project{
		{Path: "api/gateway", Dir: "/tmp/api/gateway"},
		{Path: "api/auth", Dir: "/tmp/api/auth"},
		{Path: "web/dashboard", Dir: "/tmp/web/dashboard"},
	}
	scored := ScoreProjects(all, []string{"dash"})
	require.Len(t, scored, 1, "expected unique match web/dashboard")
	assert.Equal(t, "web/dashboard", scored[0].P.Path)
}

func TestWhereAmbiguous(t *testing.T) {
	all := []*types.Project{
		{Path: "api/gateway", Dir: "/tmp/api/gateway"},
		{Path: "api/auth", Dir: "/tmp/api/auth"},
	}
	scored := ScoreProjects(all, []string{"api"})
	assert.GreaterOrEqual(t, len(scored), 2, "expected ambiguous results")
}

func TestWhereNoMatch(t *testing.T) {
	all := []*types.Project{
		{Path: "api/gateway", Dir: "/tmp/api/gateway"},
	}
	scored := ScoreProjects(all, []string{"zzznope"})
	assert.Empty(t, scored)
}

// whereRoot lays down a workspace `magus where` can walk: one nested source file and one
// project directory, enough for both matchers the command consults.
func whereRoot(t *testing.T) string {
	t.Helper()
	w := testkit.NewWorkspace(t)
	w.Write("cmd/magus/guard_shell.go", "package interactive\n")
	return w.Root()
}

// `magus where` normalizes its filters through the same function `magus query` does, so
// the shapes a human produces by copy-pasting reach the file the bare relative form does.
// The command applies it before both matchers; this covers the file half.
func TestWhereFileFiltersAcceptPastedShapes(t *testing.T) {
	root := whereRoot(t)
	abs := filepath.Join(root, "cmd", "magus", "guard_shell.go")

	for _, tc := range []struct {
		name   string
		filter string
	}{
		{"bare relative", "cmd/magus/guard_shell.go"},
		{"bare leaf", "guard_shell.go"},
		{"dot relative", "./cmd/magus/guard_shell.go"},
		{"root anchored", "/cmd/magus/guard_shell.go"},
		{"absolute", abs},
		{"backslash", `cmd\magus\guard_shell.go`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SearchFiles(t.Context(), root, NormalizeFilters([]string{tc.filter}, root), nil)
			require.NoError(t, err)
			require.Lenf(t, got, 1, "%q should find the file", tc.filter)
			assert.Equal(t, ScoredFile{Path: "cmd/magus/guard_shell.go", Score: got[0].Score}, got[0])
		})
	}
}

// The project half: `./console` is the same project `console` is.
func TestWhereProjectFiltersAcceptPastedShapes(t *testing.T) {
	root := whereRoot(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "console"), 0o755))
	all := []*types.Project{{Path: "console", Dir: filepath.Join(root, "console")}}

	for _, in := range []string{"console", "./console", "/console", filepath.Join(root, "console")} {
		scored := ScoreProjects(all, NormalizeFilters([]string{in}, root))
		require.Lenf(t, scored, 1, "%q should resolve the project", in)
		assert.Equal(t, "console", scored[0].P.Path)
	}
}

// A filter naming a path in a DIFFERENT checkout must not be re-rooted into this one: it
// stays the literal token, matches nothing, and `where` reports no match.
func TestWhereRejectsOutOfWorkspacePath(t *testing.T) {
	root := whereRoot(t)
	const filter = "/somewhere/else/cmd/magus/guard_shell.go"
	assert.Equal(t, []string{filter}, NormalizeFilters([]string{filter}, root),
		"a path from another checkout stays the literal token it was")

	got, err := SearchFiles(t.Context(), root, NormalizeFilters([]string{filter}, root), nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestWhereMatchesJoinTheWorkspaceRoot(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "ws")

	files := FileMatches(root, []ScoredFile{{Path: "cmd/x.go"}})
	assert.Equal(t, []WhereMatch{{Path: filepath.Join(root, "cmd/x.go"), Rel: "cmd/x.go", Kind: "file"}}, files)

	projects := ProjectMatches(root, []ScoredProject{{P: &types.Project{Path: "api"}}})
	assert.Equal(t, []WhereMatch{{Path: filepath.Join(root, "api"), Rel: "api", Kind: "project"}}, projects)

	var b strings.Builder
	PrintWherePaths(&b, append(files, projects...))
	assert.Equal(t, filepath.Join(root, "cmd/x.go")+"\n"+filepath.Join(root, "api")+"\n", b.String())
}
