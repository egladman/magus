package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func owner(id string, state DelegationState, paths ...string) DelegationUnit {
	return DelegationUnit{ID: id, State: state, OwnedPaths: paths}
}

func TestDelegationOverlaps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		units []DelegationUnit
		want  []DelegationOverlap
	}{
		{
			name:  "the same path claimed twice",
			units: []DelegationUnit{owner("a", StateRunning, "internal/ledger"), owner("b", StateDeclared, "internal/ledger")},
			want:  []DelegationOverlap{{A: "a", B: "b", Paths: []string{"internal/ledger"}}},
		},
		{
			// The common case a reader most needs told: one unit owns the directory, the
			// other owns a file inside it, and nothing about either row says so.
			name:  "a file inside a claimed directory, and both declarations are named",
			units: []DelegationUnit{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledger/store.go")},
			want:  []DelegationOverlap{{A: "a", B: "b", Paths: []string{"internal/ledger", "internal/ledger/store.go"}}},
		},
		{
			name:  "a glob is judged by the directories it names",
			units: []DelegationUnit{owner("a", StateRunning, "console/src/**/*.ts"), owner("b", StateRunning, "console/src/console/plan/main.ts")},
			want:  []DelegationOverlap{{A: "a", B: "b", Paths: []string{"console/src/**/*.ts", "console/src/console/plan/main.ts"}}},
		},
		{
			name:  "sibling directories are not an overlap",
			units: []DelegationUnit{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledgerx", "console/")},
		},
		{
			// A prefix that stops mid-segment is a different directory, not a parent.
			name:  "a shared name prefix inside a segment is not containment",
			units: []DelegationUnit{owner("a", StateRunning, "internal/led"), owner("b", StateRunning, "internal/ledger/store.go")},
		},
		{
			name:  "a read-only unit declares no paths, so it collides with nothing",
			units: []DelegationUnit{owner("a", StateRunning, "internal/ledger"), {ID: "scout", State: StateRunning, ReadOnly: true}},
		},
		{
			// The pair is reported once, and the ids read in ledger order so a reader can
			// find both rows in the table they are looking at.
			name: "three units claiming one tree are three pairs, each named once",
			units: []DelegationUnit{
				owner("a", StateRunning, "internal"),
				owner("b", StateRunning, "internal/ledger"),
				owner("c", StateDeclared, "internal/handler"),
			},
			want: []DelegationOverlap{
				{A: "a", B: "b", Paths: []string{"internal", "internal/ledger"}},
				{A: "a", B: "c", Paths: []string{"internal", "internal/handler"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, DelegationOverlaps(tt.units))
		})
	}
}

// A finished unit is not competing for anything. The skill has a worker RELEASE its
// paths when it stops editing, so reporting a pass, a fail, or a no-return as a
// collision would make the surface loudest exactly as the plan winds down.
func TestDelegationOverlapsSkipsTerminalUnits(t *testing.T) {
	t.Parallel()

	live := owner("live", StateRunning, "internal/ledger")
	for _, state := range []DelegationState{StatePass, StateFail, StateNoReturn} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			done := owner("done", state, "internal/ledger")
			assert.Empty(t, DelegationOverlaps([]DelegationUnit{live, done}))
			assert.Empty(t, DelegationOverlaps([]DelegationUnit{done, live}), "whichever order the rows sit in")
		})
	}
}

func TestNewDelegationReportDerivesOverlapsFromTheRows(t *testing.T) {
	t.Parallel()

	units := []DelegationUnit{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledger")}
	report := NewDelegationReport(units)
	assert.Equal(t, units, report.Units, "the rows are served exactly as they were recorded")
	assert.Len(t, report.Overlaps, 1)

	// Derived on read: the same rows with one of them finished report nothing, and no
	// row had to be rewritten for that to happen.
	units[1].State = StatePass
	assert.Empty(t, NewDelegationReport(units).Overlaps)
}
