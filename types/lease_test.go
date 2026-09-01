package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestValidLeaseID pins the rule every lease channel shares. The marker scanner is not the
// only producer: whatever stamps a Lease has to agree with this, or internal/trail's
// redaction exemption starts covering strings nobody checked.
func TestValidLeaseID(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		id   string
		want bool
	}{
		"plain":               {"MGS1021", true},
		"every separator":     {"a.b:c_d-1/2", true},
		"branch shaped":       {"feat/spawn-capture", true},
		"at the length cap":   {strings.Repeat("u", MaxLeaseIDLen), true},
		"past the length cap": {strings.Repeat("u", MaxLeaseIDLen+1), false},
		"empty":               {"", false},
		"space":               {"two words", false},
		"punctuation":         {"MGS1021!", false},
		"newline":             {"lease\n", false},
		"non-ascii":           {"lease-é", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ValidLeaseID(tc.id))
		})
	}
}

func owner(id string, state LeaseState, paths ...string) Lease {
	return Lease{ID: id, State: state, OwnedPaths: paths}
}

func TestLeaseOverlaps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		leases []Lease
		want   []LeaseOverlap
	}{
		{
			name:   "the same path claimed twice",
			leases: []Lease{owner("a", StateRunning, "internal/ledger"), owner("b", StateDeclared, "internal/ledger")},
			want: []LeaseOverlap{
				{LeaseA: "a", LeaseB: "b", PathsA: []string{"internal/ledger"}, PathsB: []string{"internal/ledger"}},
			},
		},
		{
			// The common case a reader most needs told: one lease owns the directory, the
			// other owns a file inside it, and nothing about either row says so.
			name:   "a file inside a claimed directory, and both declarations are named",
			leases: []Lease{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledger/store.go")},
			want: []LeaseOverlap{
				{
					LeaseA: "a", LeaseB: "b",
					PathsA: []string{"internal/ledger"}, PathsB: []string{"internal/ledger/store.go"},
				},
			},
		},
		{
			name:   "a glob is judged by the directories it names",
			leases: []Lease{owner("a", StateRunning, "console/src/**/*.ts"), owner("b", StateRunning, "console/src/console/plan/main.ts")},
			want: []LeaseOverlap{
				{
					LeaseA: "a", LeaseB: "b",
					PathsA: []string{"console/src/**/*.ts"},
					PathsB: []string{"console/src/console/plan/main.ts"},
				},
			},
		},
		{
			name:   "sibling directories are not an overlap",
			leases: []Lease{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledgerx", "console/")},
		},
		{
			// A prefix that stops mid-segment is a different directory, not a parent.
			name:   "a shared name prefix inside a segment is not containment",
			leases: []Lease{owner("a", StateRunning, "internal/led"), owner("b", StateRunning, "internal/ledger/store.go")},
		},
		{
			// A blank entry names nothing. It cleans to ".", and reading THAT as a claim on
			// the whole tree paired the row holding it with every other lease in the plan.
			name:   "a blank declaration claims nothing, not everything",
			leases: []Lease{owner("a", StateRunning, "  ", ""), owner("b", StateRunning, "internal/ledger")},
		},
		{
			name:   "a read-only lease declares no paths, so it collides with nothing",
			leases: []Lease{owner("a", StateRunning, "internal/ledger"), {ID: "scout", State: StateRunning, ReadOnly: true}},
		},
		{
			// The pair is reported once, and the ids read in ledger order so a reader can
			// find both rows in the table they are looking at.
			name: "three leases claiming one tree are three pairs, each named once",
			leases: []Lease{
				owner("a", StateRunning, "internal"),
				owner("b", StateRunning, "internal/ledger"),
				owner("c", StateDeclared, "internal/handler"),
			},
			want: []LeaseOverlap{
				{LeaseA: "a", LeaseB: "b", PathsA: []string{"internal"}, PathsB: []string{"internal/ledger"}},
				{LeaseA: "a", LeaseB: "c", PathsA: []string{"internal"}, PathsB: []string{"internal/handler"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, leaseOverlaps(tt.leases))
		})
	}
}

// A finished lease is not competing for anything. The skill has a worker RELEASE its
// paths when it stops editing, so reporting a pass, a fail, or a no-return as a
// collision would make the surface loudest exactly as the plan winds down.
func TestLeaseOverlapsSkipsTerminalLeases(t *testing.T) {
	t.Parallel()

	live := owner("live", StateRunning, "internal/ledger")
	for _, state := range []LeaseState{StatePass, StateFail, StateNoReturn} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			done := owner("done", state, "internal/ledger")
			assert.Empty(t, leaseOverlaps([]Lease{live, done}))
			assert.Empty(t, leaseOverlaps([]Lease{done, live}), "whichever order the rows sit in")
		})
	}
}

func TestNewLeaseReportDerivesOverlapsFromTheRows(t *testing.T) {
	t.Parallel()

	leases := []Lease{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledger")}
	report := NewLeaseReport(leases)
	assert.Equal(t, leases, report.Leases, "the rows are served exactly as they were recorded")
	assert.Len(t, report.Overlaps, 1)

	// Derived on read: the same rows with one of them finished report nothing, and no
	// row had to be rewritten for that to happen.
	leases[1].State = StatePass
	assert.Empty(t, NewLeaseReport(leases).Overlaps)
}

// The empty case is settled in the constructor rather than at each read door, so the MCP
// tool and the console's route cannot disagree about it: an unwritten ledger serves an
// empty list, never null, whichever door served it.
func TestNewLeaseReportNormalizesTheEmptyLedger(t *testing.T) {
	t.Parallel()

	report := NewLeaseReport(nil)
	assert.NotNil(t, report.Leases, "a workspace where nobody has declared a lease yet is empty, not broken")
	assert.Empty(t, report.Leases)
	assert.Empty(t, report.Overlaps)
}

// Clone is what makes a row handed out of a store safe to keep, so a slice field it
// forgets aliases the store's own array. Every field here carries SPARE CAPACITY, which is
// the shape the ledger actually stores (RecordUnattributedWrite sizes its slice for one
// more than it fills): an append then lands in place, and the damage is invisible to the
// appender - it shows up as the NEXT reader's append overwriting the first one's row. So
// this asserts across two clones rather than back at the original, which is the only form
// of the check that can fail.
func TestLeaseCloneCopiesEverySliceField(t *testing.T) {
	t.Parallel()

	orig := Lease{
		ID:             "a",
		OwnedPaths:     append(make([]string, 0, 4), "types/"),
		ForbiddenPaths: append(make([]string, 0, 4), "gen/"),
		DependsOn:      append(make([]string, 0, 4), "b"),
		Releases:       append(make([]LeaseRelease, 0, 4), LeaseRelease{Path: "types/x.go"}),
		Unattributed:   append(make([]LeaseUnattributedWrite, 0, 4), LeaseUnattributedWrite{Path: "types/y.go"}),
	}

	first, second := orig.Clone(), orig.Clone()
	first.OwnedPaths = append(first.OwnedPaths, "first/")
	first.ForbiddenPaths = append(first.ForbiddenPaths, "first/")
	first.DependsOn = append(first.DependsOn, "first")
	first.Releases = append(first.Releases, LeaseRelease{Path: "first/z.go"})
	first.Unattributed = append(first.Unattributed, LeaseUnattributedWrite{Path: "first/z.go"})

	second.OwnedPaths = append(second.OwnedPaths, "second/")
	second.ForbiddenPaths = append(second.ForbiddenPaths, "second/")
	second.DependsOn = append(second.DependsOn, "second")
	second.Releases = append(second.Releases, LeaseRelease{Path: "second/z.go"})
	second.Unattributed = append(second.Unattributed, LeaseUnattributedWrite{Path: "second/z.go"})

	assert.Equal(t, []string{"types/", "first/"}, first.OwnedPaths)
	assert.Equal(t, []string{"gen/", "first/"}, first.ForbiddenPaths)
	assert.Equal(t, []string{"b", "first"}, first.DependsOn)
	assert.Equal(t, []LeaseRelease{{Path: "types/x.go"}, {Path: "first/z.go"}}, first.Releases)
	assert.Equal(t, []LeaseUnattributedWrite{{Path: "types/y.go"}, {Path: "first/z.go"}}, first.Unattributed)

	// The original is the store's row and nobody appended through it, so it must still
	// hold exactly what it held.
	assert.Equal(t, []string{"types/"}, orig.OwnedPaths)
	assert.Equal(t, []LeaseUnattributedWrite{{Path: "types/y.go"}}, orig.Unattributed)
}

// slices.Clone preserves nil, which is what keeps a row that stored null from coming back
// as [] through the JSON door.
func TestLeaseCloneKeepsNilSlicesNil(t *testing.T) {
	t.Parallel()

	c := Lease{ID: "a"}.Clone()
	assert.Nil(t, c.Unattributed)
	assert.Nil(t, c.Releases)
}
