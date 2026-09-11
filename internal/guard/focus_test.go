package guard

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// focusFixture is a WorkspaceReader over the same shapes project's own focus test
// grades: an upstream dependency, a sibling, and a reverse dependent. It is a fake
// rather than a real checkout because these cases are about the VERDICT, and
// standing up a magusfile to reach one would test workspace discovery instead.
type focusFixture struct {
	root     string
	projects map[string]*types.Project
}

func (w focusFixture) Root() string { return w.root }

func (w focusFixture) All() []*types.Project {
	out := make([]*types.Project, 0, len(w.projects))
	for _, p := range w.projects {
		out = append(out, p)
	}
	return out
}

func (w focusFixture) Get(path string) *types.Project      { return w.projects[path] }
func (w focusFixture) Graph() (*types.Graph, error)        { panic("focus must not build the graph") }
func (w focusFixture) VCSOptions() types.VCSOptions        { panic("unused") }
func (w focusFixture) Where(string) (*types.Project, bool) { panic("unused") }

func newFocusFixture() focusFixture {
	w := focusFixture{root: "/ws", projects: map[string]*types.Project{}}
	for _, p := range []*types.Project{
		{Path: ".", Name: "root"},
		{Path: "app", DependsOn: []string{"libs/core"}},
		{Path: "libs/core"},
		{Path: "libs/ui"},
		{Path: "web", DependsOn: []string{"app"}},
	} {
		w.projects[p.Path] = p
	}
	return w
}

func appFocus(t *testing.T) project.Focus {
	t.Helper()
	f, ok := project.FocusAt(newFocusFixture(), "/ws/app")
	require.True(t, ok)
	return f
}

func TestFocusReadOperands(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		want    []string
	}{
		{"cat", "cat libs/ui/theme.css", []string{"libs/ui/theme.css"}},
		{"sed range", "sed -n 1,80p libs/ui/theme.css", []string{"libs/ui/theme.css"}},
		{"head with a byte count", "head -c 200 libs/ui/theme.css", []string{"libs/ui/theme.css"}},
		{"grep drops its pattern", "grep -rn foo/bar libs/ui", []string{"libs/ui"}},
		{"rg drops its pattern", "rg 'func Load' web/server", []string{"web/server"}},
		{"a pipeline is still two commands", "cat app/a.go | grep x web/b.go", []string{"app/a.go", "web/b.go"}},
		{"find walks its first operand", "find libs/ui -name '*.css'", []string{"libs/ui"}},
		// The shapes that must stay silent, because a false positive here is an
		// advisory about a path the command never read.
		{"a bare pattern is not a path", "grep -rn TODO", nil},
		{"a bare name is a root file, and shared", "cat README.md", nil},
		{"not a reader", "go build ./libs/ui/...", nil},
		{"stdin has no operand", "cat", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmds, ok := ParseCommands(tc.command)
			require.True(t, ok)
			assert.Equal(t, tc.want, focusReadOperands(cmds))
		})
	}
}

func TestFocusVerdictAdvisesWithoutALease(t *testing.T) {
	got := focusVerdict(appFocus(t), "", "/ws", "/ws/app", []string{"../libs/ui/theme.css"})
	assert.Equal(t, "advise", got.Decision)
	assert.Equal(t, "libs/ui/theme.css", got.Rel)
	assert.Contains(t, got.Context, "belongs to project libs/ui")
	assert.Contains(t, got.Context, "outside this session's focus")
	assert.Contains(t, got.Context, "magus describe file libs/ui/theme.css")
	assert.Contains(t, got.Brief, "libs/ui/theme.css is outside app's focus")
	assert.NotContains(t, got.Brief, "\n", "a repeat is one line or it is not a repeat")
}

func TestFocusVerdictStaysSilentInsideTheFocus(t *testing.T) {
	focus := appFocus(t)
	for _, op := range []string{
		"app/main.go",              // the project itself
		"../libs/core/core.go",     // a declared depends_on
		"../MAGUS.md",              // a workspace-root file
		"../.claude/skills/x/S.md", // the session's own instructions
		"/elsewhere/other/x.go",    // outside the workspace: a different rule's business
	} {
		assert.Empty(t, focusVerdict(focus, "", "/ws", "/ws/app", []string{op}).Decision, op)
	}
}

func TestFocusVerdictDeniesUnderALease(t *testing.T) {
	focus, ok := project.FocusForPaths(newFocusFixture(), []string{"app/**"})
	require.True(t, ok)

	got := focusVerdict(focus, "lease-a", "/ws", "/ws", []string{"web/server.go"})
	assert.Equal(t, "deny", got.Decision)
	assert.Contains(t, got.Reason, "read inside the focus lease lease-a was given (app)")
	// The actor, not the tool: naming the tool reads as permission, and two personas
	// widened their own row on it.
	assert.Contains(t, got.Reason, "Your orchestrator can widen this lane; you cannot.")
	assert.NotContains(t, got.Reason, hint.ToolLedger.String())
	assert.Contains(t, got.Reason, "not inventing a rule")
	assert.Empty(t, got.Context, "a deny carries its reason, never a context the host would inject alongside it")

	// The same lease reading its own dependency is not a boundary crossing.
	assert.Empty(t, focusVerdict(focus, "lease-a", "/ws", "/ws", []string{"libs/core/core.go"}).Decision)
}

func TestFocusVerdictWideningIsWhatTheLeaseDeclares(t *testing.T) {
	// The write lane alone denies the read...
	narrow, ok := project.FocusForPaths(newFocusFixture(), []string{"app/**"})
	require.True(t, ok)
	assert.Equal(t, "deny", focusVerdict(narrow, "lease-a", "/ws", "/ws", []string{"libs/ui/theme.css"}).Decision)

	// ...and a declared focus is the one thing that opens it, without handing the
	// lease the right to WRITE what it may now read.
	wide, ok := project.FocusForPaths(newFocusFixture(), []string{"app/**", "libs/ui"})
	require.True(t, ok)
	assert.Empty(t, focusVerdict(wide, "lease-a", "/ws", "/ws", []string{"libs/ui/theme.css"}).Decision)
}

func TestFocusVerdictReportsTheFirstOperandThatLeaves(t *testing.T) {
	got := focusVerdict(appFocus(t), "", "/ws", "/ws/app", []string{"app/a.go", "../web/b.go", "../libs/ui/c.css"})
	assert.Equal(t, "advise", got.Decision)
	assert.Equal(t, "web/b.go", got.Rel, "one explanation per command, not one per operand")
}

// TestAdvisoryFocusPathIsPerPath pins the marker key the once-per-path dedupe rides
// on. Every other enrolled advisory reports a standing fact and needs one key for
// the whole session; this one has a different subject on every firing, and a shared
// key would silence every out-of-focus path after the first.
func TestAdvisoryFocusPathIsPerPath(t *testing.T) {
	a, b := advisoryFocusPath("libs/ui/theme.css"), advisoryFocusPath("web/server.go")
	assert.NotEqual(t, a, b)
	assert.Equal(t, a, advisoryFocusPath("libs/ui/theme.css"), "the same path keys the same marker")
	assert.NotEqual(t, advisoryFocus, a, "and neither spends the kind's own one firing")

	base := t.TempDir()
	markers := hint.NewGate(base, "session-1")
	assert.False(t, markers.MarkFired(a), "first sighting of a path speaks")
	assert.True(t, markers.MarkFired(a), "the second is the same fact")
	assert.False(t, markers.MarkFired(b), "a second path is a second fact")
}

func TestFocusGradeSaysNothingWithoutAWorkspace(t *testing.T) {
	// A context pinned to an empty location is a hook that could not find a
	// workspace, which is the uncertainty every guard rule answers with silence.
	ctx := context.WithValue(t.Context(), locationKey{}, location{})
	assert.Empty(t, gradeFocusRead(ctx, Deps{}, "", "cat libs/ui/theme.css").Decision)
	assert.Empty(t, gradeFocusRead(ctx, Deps{}, "", "").Decision)
	assert.Empty(t, gradeFocusRead(ctx, Deps{}, "", "cat 'unterminated").Decision)
}
