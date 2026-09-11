package guard

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/types"
)

// The whole policy, over the same fixture the focus rule is graded against: an
// upstream dependency, a sibling, and a reverse dependent. A sibling is the only
// shape that fires, and it is the one the graph says nothing connects.
func TestDriftVerdict(t *testing.T) {
	for _, tc := range []struct {
		name    string
		write   string
		touched []string
		project string
		fires   bool
	}{
		{
			name:    "a first write has no scope to have drifted from",
			write:   "/ws/libs/ui/button.ts",
			project: "libs/ui",
		},
		{
			name:    "the same project again",
			write:   "/ws/libs/ui/input.ts",
			touched: []string{"libs/ui"},
			project: "libs/ui",
		},
		{
			name:    "a project the session's work depends on",
			write:   "/ws/libs/core/parse.go",
			touched: []string{"app"},
			project: "libs/core",
		},
		{
			name:    "a project that depends on the session's work",
			write:   "/ws/web/page.ts",
			touched: []string{"libs/core"},
			project: "web",
		},
		{
			name:    "a sibling no edge reaches in either direction",
			write:   "/ws/libs/ui/button.ts",
			touched: []string{"app"},
			project: "libs/ui",
			fires:   true,
		},
		{
			name:    "related to one of several touched projects is related",
			write:   "/ws/libs/core/parse.go",
			touched: []string{"libs/ui", "app"},
			project: "libs/core",
		},
		{
			// The root project is spelled "." as a project path and resolves to no seed
			// when read as a lease declaration, which would leave the rule quiet in every
			// workspace whose root owns most of the tree.
			name:    "the touched set holds only the root project",
			write:   "/ws/libs/ui/button.ts",
			touched: []string{"."},
			project: "libs/ui",
			fires:   true,
		},
		{
			name:  "a path outside the workspace belongs to no project",
			write: "/elsewhere/x.go",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drift := driftVerdict(newFocusFixture(), tc.write, tc.touched)
			assert.Equal(t, tc.project, drift.project)
			assert.Equal(t, tc.fires, drift.advice != "", "advice: %q", drift.advice)
		})
	}
}

// The text has to carry both halves of the split it is proposing, or the reader
// cannot tell which two units it means, and one runnable command that checks it.
func TestScopeDriftAdviceNamesBothSides(t *testing.T) {
	advice := driftVerdict(newFocusFixture(), "/ws/libs/ui/button.ts", []string{"app"}).advice

	assert.Contains(t, advice, "libs/ui")
	assert.Contains(t, advice, "app")
	assert.Contains(t, advice, "magus path libs/ui app", "the advisory has to name a command the reader can check it with")
	assert.Contains(t, advice, "magus-multi-agent", "naming a skill magus itself installs, or the routing lands in a wall")
	assert.NotContains(t, advice, " - ", "comment and message prose carries no spaced-hyphen asides")
}

// Once per (session, project): the recorded set is what holds it, so the second
// write to the same new project never reaches the graph at all.
func TestScopeDriftFiresOncePerProject(t *testing.T) {
	markers := hint.NewGate(t.TempDir(), "session-1")
	ws := newFocusFixture()

	scopeDrift{markers: markers, project: "app"}.record()

	drift := driftVerdict(ws, "/ws/libs/ui/button.ts", touchedProjects(markers))
	require.NotEmpty(t, drift.advice, "a sibling of the only touched project is owed the advisory once")
	drift.markers = markers
	drift.record()

	repeat := driftVerdict(ws, "/ws/libs/ui/input.ts", touchedProjects(markers))
	assert.Empty(t, repeat.advice, "the project is in the session's set now, so the second write is in scope")
	assert.Equal(t, []string{"app", "libs/ui"}, touchedProjects(markers))
}

// A gate with no cache base remembers nothing, and a rule that cannot remember must
// not pretend the session has touched something.
func TestScopeDriftRecordsNothingWithoutABase(t *testing.T) {
	markers := hint.NewGate("", "session-1")
	scopeDrift{markers: markers, project: "app"}.record()

	assert.Empty(t, touchedProjects(markers))
}

// A worker writing inside the lane its orchestrator declared is in scope by
// declaration, whatever the graph says about the projects that lane spans.
func TestLeaseCoversWrite(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	base, root := t.TempDir(), t.TempDir()
	location := location{cacheDir: base, workspace: root}
	store := ledger.NewStore(ledger.Location{CacheDir: base, Root: root})
	_, err := store.Put(t.Context(), types.Lease{
		ID:         "unit-a",
		State:      types.StateRunning,
		WritePaths: []string{"libs/ui", "app/api"},
	})
	require.NoError(t, err)

	for _, tc := range []struct {
		name  string
		lease string
		write string
		want  bool
	}{
		{name: "inside a declared path", lease: "unit-a", write: filepath.Join(root, "libs/ui/button.ts"), want: true},
		{name: "outside every declared path", lease: "unit-a", write: filepath.Join(root, "libs/core/parse.go")},
		{name: "no lease acting", write: filepath.Join(root, "libs/ui/button.ts")},
		{name: "a lease the ledger does not hold", lease: "unit-b", write: filepath.Join(root, "libs/ui/button.ts")},
		{name: "outside the workspace", lease: "unit-a", write: "/elsewhere/x.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, leaseCoversWrite(tc.lease, location, tc.write))
		})
	}
}
