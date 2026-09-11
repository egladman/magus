package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// focusWorkspace is a WorkspaceReader over a fixture project set. Only the three
// readers focus uses have bodies; the rest exist to satisfy the interface and
// panic rather than return a zero value, so a focus rule that starts consulting
// the graph fails loudly here instead of silently reading an empty one.
type focusWorkspace struct {
	root     string
	projects map[string]*types.Project
}

func (w focusWorkspace) Root() string { return w.root }

func (w focusWorkspace) All() []*types.Project {
	out := make([]*types.Project, 0, len(w.projects))
	for _, p := range w.projects {
		out = append(out, p)
	}
	return out
}

func (w focusWorkspace) Get(path string) *types.Project { return w.projects[path] }

func (w focusWorkspace) Graph() (*types.Graph, error) { panic("focus must not build the graph") }

func (w focusWorkspace) VCSOptions() types.VCSOptions { panic("unused") }

func (w focusWorkspace) Where(string) (*types.Project, bool) { panic("unused") }

// fixture is one workspace with every shape a focus verdict has to separate:
// an upstream dependency, a nested project, a sibling, and a reverse dependent.
//
//	.            root
//	app          -> libs/core
//	app/plugin   nested inside app
//	libs/core
//	libs/ui      sibling of libs/core, depended on by nobody in app's closure
//	web          -> app, so app's REVERSE dependent
func fixture() focusWorkspace {
	w := focusWorkspace{root: "/ws", projects: map[string]*types.Project{}}
	for _, p := range []*types.Project{
		{Path: ".", Name: "root"},
		{Path: "app", DependsOn: []string{"libs/core"}},
		{Path: "app/plugin"},
		{Path: "libs/core"},
		{Path: "libs/ui"},
		{Path: "web", DependsOn: []string{"app"}},
	} {
		w.projects[p.Path] = p
	}
	return w
}

func TestFocusAtIncludesUpstreamAndNestingOnly(t *testing.T) {
	f, ok := FocusAt(fixture(), "/ws/app")
	require.True(t, ok)
	assert.Equal(t, []string{"app"}, f.Seeds)
	assert.Equal(t, []string{"app", "app/plugin", "libs/core"}, f.Projects,
		"focus runs upstream and into nested projects, never sideways or into dependents")
}

func TestFocusContains(t *testing.T) {
	f, ok := FocusAt(fixture(), "/ws/app/handlers")
	require.True(t, ok)

	for _, tc := range []struct {
		path string
		want bool
		why  string
	}{
		{"app/main.go", true, "the project the session stands in"},
		{"app/plugin/p.go", true, "a project nested inside it is inside what you were pointed at"},
		{"libs/core/core.go", true, "a declared depends_on is an input to this project's work"},
		{"libs/ui/ui.go", false, "a sibling nobody in the closure depends on"},
		{"web/server.go", false, "a reverse dependent: affected runs this way, focus does not"},
		{"README.md", true, "a file at depth zero describes the workspace"},
		{"magusfile.buzz", true, "the workspace declaration every project resolves through"},
		{".claude/skills/magus-run/SKILL.md", true, "the session's own instructions"},
		{"vendor/x/y.go", true, "only the root catches it, so no lane it could be outside of"},
	} {
		assert.Equal(t, tc.want, f.Contains(tc.path), "%s: %s", tc.path, tc.why)
	}
}

func TestFocusAtRootProjectExcludesEveryOtherProject(t *testing.T) {
	f, ok := FocusAt(fixture(), "/ws")
	require.True(t, ok)
	assert.Equal(t, []string{"."}, f.Projects,
		"every project is nested in the root, so treating the root's descendants as focus "+
			"would make a root-level session's focus the whole workspace")
	assert.False(t, f.Contains("app/main.go"))
	assert.True(t, f.Contains("cmd/tool/main.go"), "the root project's own tree")
}

func TestFocusAtOutsideTheWorkspaceReportsNoFocus(t *testing.T) {
	_, ok := FocusAt(fixture(), "/elsewhere/repo")
	assert.False(t, ok, "a caller with no focus must stay silent rather than guess")
}

func TestFocusForPathsSeedsFromDeclarations(t *testing.T) {
	f, ok := FocusForPaths(fixture(), []string{"app/**", "libs/ui/theme/*.css"})
	require.True(t, ok)
	assert.Equal(t, []string{"app", "libs/ui"}, f.Seeds,
		"a glob names its project by its literal prefix")
	assert.Equal(t, []string{"app", "app/plugin", "libs/core", "libs/ui"}, f.Projects)
	assert.False(t, f.Contains("web/server.go"))
}

func TestFocusForPathsWithNothingItCanAttribute(t *testing.T) {
	w := focusWorkspace{root: "/ws", projects: map[string]*types.Project{"app": {Path: "app"}}}
	_, ok := FocusForPaths(w, []string{"docs/guide.md", "../outside"})
	assert.False(t, ok, "no project owns either path, so there is no lane to compute")
}

func TestFocusOwnerPrefersTheInnermostProject(t *testing.T) {
	f, ok := FocusAt(fixture(), "/ws/app")
	require.True(t, ok)
	assert.Equal(t, "app/plugin", f.Owner("app/plugin/deep/file.go"),
		"the longest project path wins, the same rule ClassifyFiles attributes by")
	assert.Equal(t, ".", f.Owner("cmd/tool/main.go"))
}

func TestFocusForPathsReadsGlobDeclarationsThroughTypes(t *testing.T) {
	// The lease's own prefix rule, not a second one here: a declaration that names a
	// project in the job store has to name the same project in the focus.
	f, ok := FocusForPaths(fixture(), []string{"app/plugin/**/*.go", "**"})
	require.True(t, ok)
	assert.Equal(t, []string{"app/plugin"}, f.Seeds,
		`"**" names no directory, so it seeds nothing`)
}
