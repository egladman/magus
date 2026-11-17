package describe

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// TestClaimLabel pins the one spelling `describe file` prints a claim's declarer
// as: the path:target ref every other magus command accepts, so a reader can run
// the target that wrote the file without translating anything.
func TestClaimLabel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		claim types.FileClaim
		want  string
	}{
		{name: "per-target glob", claim: types.FileClaim{Project: "docs", Target: "generate"}, want: "docs:generate"},
		{name: "project-wide glob", claim: types.FileClaim{Project: "docs"}, want: "docs"},
		{name: "root project", claim: types.FileClaim{Project: ".", Target: "generate"}, want: ".:generate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ClaimLabel(tc.claim))
		})
	}
}

// focusFixture is a WorkspaceReader over the same shapes project's own focus test
// grades: an upstream dependency, a sibling, and a reverse dependent. It is a fake
// rather than a real checkout because TestMarkFileFocusStampsEachEntry is about the
// VERDICT, and standing up a magusfile to reach one would test workspace discovery
// instead. Mirrors internal/guard's own focusFixture, unexported there.
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

// TestMarkFileFocusStampsEachEntry covers the `magus describe file` field: the same
// computation the guard grades with, surfaced where a caller can ask about a path
// before reading it rather than after being advised.
func TestMarkFileFocusStampsEachEntry(t *testing.T) {
	root := testkit.NewWorkspace(t).Root()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "app"), 0o755))
	t.Chdir(filepath.Join(root, "app"))

	ws := newFocusFixture()
	ws.root = root
	files := []types.FileEntry{
		{Path: "app/main.go"},
		{Path: "libs/core/core.go"},
		{Path: "libs/ui/theme.css"},
		{Path: "MAGUS.md"},
	}
	focus, ok := MarkFileFocus(ws, files)
	require.True(t, ok)
	assert.Equal(t, []string{"app"}, focus.Seeds)
	assert.Equal(t, []string{types.FocusIn, types.FocusIn, types.FocusOut, types.FocusIn},
		[]string{files[0].Focus, files[1].Focus, files[2].Focus, files[3].Focus},
		"the sibling is the only one out; a root file and a declared dependency are in")
}

func TestFirstLine(t *testing.T) {
	assert.Equal(t, "", FirstLine(""))
	assert.Equal(t, "one", FirstLine("one"))
	assert.Equal(t, "one", FirstLine("one\ntwo\nthree"))
	assert.Equal(t, "", FirstLine("\ntwo"))
}
func TestFilterByNameAndNamesOf(t *testing.T) {
	items := []types.SpellVersion{{Tool: "go"}, {Tool: "node"}}
	nameOf := func(v types.SpellVersion) string { return v.Tool }

	assert.Equal(t, []types.SpellVersion{{Tool: "node"}}, FilterByName(items, "node", nameOf))
	assert.Nil(t, FilterByName(items, "rustc", nameOf))
	assert.Equal(t, []string{"go", "node"}, CollectNames(items, nameOf))
	assert.Empty(t, CollectNames(nil, nameOf))
}

// TestUnknownEntitySuggestsAndExitsTwo pins both halves: a near miss earns a suggestion,
// and the caller gets the already-printed sentinel so the message is not doubled.
func TestUnknownEntitySuggestsAndExitsTwo(t *testing.T) {
	var err error
	out := notices(t, func() {
		err = UnknownEntity("spell", "gol", []string{"go", "typescript"})
	})

	assert.Equal(t, exit.Reported(exit.Usage), err)
	assert.Contains(t, out, `component="magus describe spell"`)
	assert.Contains(t, out, `unknown spell \"gol\"`)
	assert.Contains(t, out, `did you mean \"go\"?`)

	out = notices(t, func() {
		err = UnknownEntity("spell", "zzzzzzzz", []string{"go"})
	})
	assert.Equal(t, exit.Reported(exit.Usage), err)
	assert.NotContains(t, out, "did you mean")
}

func notices(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	fn()
	return buf.String()
}

// TestTargetSummaryPrefersTheAuthorsWords pins the fallback order: a doc comment beats a
// derived description, and a target that composes only siblings still says something.
func TestTargetSummaryPrefersTheAuthorsWords(t *testing.T) {
	assert.Equal(t, "build the site", TargetSummary(types.TargetGraphNode{
		Doc:    "build the site",
		Spells: []types.TargetSpellUse{{Spell: "go", Ops: []string{"go-build"}}},
	}))

	assert.Equal(t, "[go: go-build, go-vet; md: lint]", TargetSummary(types.TargetGraphNode{
		Spells: []types.TargetSpellUse{
			{Spell: "go", Ops: []string{"go-build", "go-vet"}},
			{Spell: "md", Ops: []string{"lint"}},
		},
	}))

	assert.Equal(t, "[needs: build, test]", TargetSummary(types.TargetGraphNode{
		Dependencies: []string{"build", "test"},
	}))

	assert.Equal(t, "", TargetSummary(types.TargetGraphNode{Name: "bare"}))
}
