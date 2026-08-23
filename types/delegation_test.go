package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestValidDelegationID pins the rule every delegation channel shares. The marker scanner is not the
// only producer: whatever stamps a Delegation has to agree with this, or internal/trail's
// redaction exemption starts covering strings nobody checked.
func TestValidDelegationID(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		id   string
		want bool
	}{
		"plain":               {"MGS1021", true},
		"every separator":     {"a.b:c_d-1/2", true},
		"branch shaped":       {"feat/spawn-capture", true},
		"at the length cap":   {strings.Repeat("u", MaxDelegationIDLen), true},
		"past the length cap": {strings.Repeat("u", MaxDelegationIDLen+1), false},
		"empty":               {"", false},
		"space":               {"two words", false},
		"punctuation":         {"MGS1021!", false},
		"newline":             {"delegation\n", false},
		"non-ascii":           {"delegation-é", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ValidDelegationID(tc.id))
		})
	}
}

func owner(id string, state DelegationState, paths ...string) Delegation {
	return Delegation{ID: id, State: state, OwnedPaths: paths}
}

func TestDelegationOverlaps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		delegations []Delegation
		want        []DelegationOverlap
	}{
		{
			name:        "the same path claimed twice",
			delegations: []Delegation{owner("a", StateRunning, "internal/ledger"), owner("b", StateDeclared, "internal/ledger")},
			want: []DelegationOverlap{
				{DelegationA: "a", DelegationB: "b", PathsA: []string{"internal/ledger"}, PathsB: []string{"internal/ledger"}},
			},
		},
		{
			// The common case a reader most needs told: one delegation owns the directory, the
			// other owns a file inside it, and nothing about either row says so.
			name:        "a file inside a claimed directory, and both declarations are named",
			delegations: []Delegation{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledger/store.go")},
			want: []DelegationOverlap{
				{
					DelegationA: "a", DelegationB: "b",
					PathsA: []string{"internal/ledger"}, PathsB: []string{"internal/ledger/store.go"},
				},
			},
		},
		{
			name:        "a glob is judged by the directories it names",
			delegations: []Delegation{owner("a", StateRunning, "console/src/**/*.ts"), owner("b", StateRunning, "console/src/console/plan/main.ts")},
			want: []DelegationOverlap{
				{
					DelegationA: "a", DelegationB: "b",
					PathsA: []string{"console/src/**/*.ts"},
					PathsB: []string{"console/src/console/plan/main.ts"},
				},
			},
		},
		{
			name:        "sibling directories are not an overlap",
			delegations: []Delegation{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledgerx", "console/")},
		},
		{
			// A prefix that stops mid-segment is a different directory, not a parent.
			name:        "a shared name prefix inside a segment is not containment",
			delegations: []Delegation{owner("a", StateRunning, "internal/led"), owner("b", StateRunning, "internal/ledger/store.go")},
		},
		{
			// A blank entry names nothing. It cleans to ".", and reading THAT as a claim on
			// the whole tree paired the row holding it with every other delegation in the plan.
			name:        "a blank declaration claims nothing, not everything",
			delegations: []Delegation{owner("a", StateRunning, "  ", ""), owner("b", StateRunning, "internal/ledger")},
		},
		{
			name:        "a read-only delegation declares no paths, so it collides with nothing",
			delegations: []Delegation{owner("a", StateRunning, "internal/ledger"), {ID: "scout", State: StateRunning, ReadOnly: true}},
		},
		{
			// The pair is reported once, and the ids read in ledger order so a reader can
			// find both rows in the table they are looking at.
			name: "three delegations claiming one tree are three pairs, each named once",
			delegations: []Delegation{
				owner("a", StateRunning, "internal"),
				owner("b", StateRunning, "internal/ledger"),
				owner("c", StateDeclared, "internal/handler"),
			},
			want: []DelegationOverlap{
				{DelegationA: "a", DelegationB: "b", PathsA: []string{"internal"}, PathsB: []string{"internal/ledger"}},
				{DelegationA: "a", DelegationB: "c", PathsA: []string{"internal"}, PathsB: []string{"internal/handler"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, delegationOverlaps(tt.delegations))
		})
	}
}

// A finished delegation is not competing for anything. The skill has a worker RELEASE its
// paths when it stops editing, so reporting a pass, a fail, or a no-return as a
// collision would make the surface loudest exactly as the plan winds down.
func TestDelegationOverlapsSkipsTerminalDelegations(t *testing.T) {
	t.Parallel()

	live := owner("live", StateRunning, "internal/ledger")
	for _, state := range []DelegationState{StatePass, StateFail, StateNoReturn} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			done := owner("done", state, "internal/ledger")
			assert.Empty(t, delegationOverlaps([]Delegation{live, done}))
			assert.Empty(t, delegationOverlaps([]Delegation{done, live}), "whichever order the rows sit in")
		})
	}
}

func TestNewDelegationReportDerivesOverlapsFromTheRows(t *testing.T) {
	t.Parallel()

	delegations := []Delegation{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledger")}
	report := NewDelegationReport(delegations)
	assert.Equal(t, delegations, report.Delegations, "the rows are served exactly as they were recorded")
	assert.Len(t, report.Overlaps, 1)

	// Derived on read: the same rows with one of them finished report nothing, and no
	// row had to be rewritten for that to happen.
	delegations[1].State = StatePass
	assert.Empty(t, NewDelegationReport(delegations).Overlaps)
}

// The empty case is settled in the constructor rather than at each read door, so the MCP
// tool and the console's route cannot disagree about it: an unwritten ledger serves an
// empty list, never null, whichever door served it.
func TestNewDelegationReportNormalizesTheEmptyLedger(t *testing.T) {
	t.Parallel()

	report := NewDelegationReport(nil)
	assert.NotNil(t, report.Delegations, "a workspace where nobody has delegated yet is empty, not broken")
	assert.Empty(t, report.Delegations)
	assert.Empty(t, report.Overlaps)
}
