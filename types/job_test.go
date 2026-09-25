package types

import (
	"reflect"
	"strings"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jobFieldNames is the golden list of every json field types.Job carries, pinned beside
// JobSchemaVersion. A field added, removed or renamed here without also bumping the
// version is exactly how two live rows lost write_paths, read_paths, deny_paths, model
// and check on 2026-09-11: an older binary's non-strict decoder dropped what it did not
// know, then rewrote the whole row without them.
//
// A Go literal here rather than a testdata fixture: the list is read by this one test and
// nothing else, so keeping it as code puts the diff a JobSchemaVersion bump requires in
// the same review as the field change, rather than in a second file a reviewer has to
// remember to open.
var jobFieldNames = []string{
	"schema_version", "id", "parent", "criteria", "checkpoint", "write_paths", "deny_paths",
	"read_paths", "depends_on", "model", "check", "validation", "completion_gates", "state", "holder",
	"read_only", "releases", "unattributed", "write_proof", "reported_base", "base_verdict",
	"registered_by", "registered", "checkout_root", "end_reason", "created", "updated", "deadline", "result", "attempt", "gate_attempts", "last_run",
}

// TestJobSchemaVersionCoversEveryField pins Job's field set against jobFieldNames, read
// off the struct with reflect so a field added straight to the struct cannot slip past
// either list unnoticed.
func TestJobSchemaVersionCoversEveryField(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeOf(Job{})
	got := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		got = append(got, name)
	}
	assert.Equal(t, jobFieldNames, got,
		"types.Job's field set changed: bump JobSchemaVersion and update jobFieldNames in types/job_test.go in the same change")
}

// TestValidJobID pins the rule every lease channel shares. The marker scanner is not the
// only producer: whatever stamps a Job has to agree with this, or internal/trail's
// redaction exemption starts covering strings nobody checked.
func TestValidJobID(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		id   string
		want bool
	}{
		"plain":               {"MGS1021", true},
		"every separator":     {"a.b:c_d-1/2", true},
		"branch shaped":       {"feat/spawn-capture", true},
		"at the length cap":   {strings.Repeat("u", MaxJobIDLen), true},
		"past the length cap": {strings.Repeat("u", MaxJobIDLen+1), false},
		"empty":               {"", false},
		"space":               {"two words", false},
		"punctuation":         {"MGS1021!", false},
		"newline":             {"lease\n", false},
		"non-ascii":           {"lease-é", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ValidJobID(tc.id))
		})
	}
}

func owner(id string, state JobState, paths ...string) Job {
	return Job{ID: id, State: state, WritePaths: paths}
}

func TestJobOverlaps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		leases []Job
		want   []JobOverlap
	}{
		{
			name:   "the same path claimed twice",
			leases: []Job{owner("a", StateRunning, "internal/ledger"), owner("b", StateDeclared, "internal/ledger")},
			want: []JobOverlap{
				{JobA: "a", JobB: "b", PathsA: []string{"internal/ledger"}, PathsB: []string{"internal/ledger"}},
			},
		},
		{
			// The common case a reader most needs told: one lease owns the directory, the
			// other owns a file inside it, and nothing about either row says so.
			name:   "a file inside a claimed directory, and both declarations are named",
			leases: []Job{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledger/store.go")},
			want: []JobOverlap{
				{
					JobA: "a", JobB: "b",
					PathsA: []string{"internal/ledger"}, PathsB: []string{"internal/ledger/store.go"},
				},
			},
		},
		{
			name:   "a glob is judged by the directories it names",
			leases: []Job{owner("a", StateRunning, "console/src/**/*.ts"), owner("b", StateRunning, "console/src/console/plan/main.ts")},
			want: []JobOverlap{
				{
					JobA: "a", JobB: "b",
					PathsA: []string{"console/src/**/*.ts"},
					PathsB: []string{"console/src/console/plan/main.ts"},
				},
			},
		},
		{
			name:   "sibling directories are not an overlap",
			leases: []Job{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledgerx", "console/")},
		},
		{
			// A prefix that stops mid-segment is a different directory, not a parent.
			name:   "a shared name prefix inside a segment is not containment",
			leases: []Job{owner("a", StateRunning, "internal/led"), owner("b", StateRunning, "internal/ledger/store.go")},
		},
		{
			// A blank entry names nothing. It cleans to ".", and reading THAT as a claim on
			// the whole tree paired the row holding it with every other lease in the plan.
			name:   "a blank declaration claims nothing, not everything",
			leases: []Job{owner("a", StateRunning, "  ", ""), owner("b", StateRunning, "internal/ledger")},
		},
		{
			name:   "a read-only lease declares no paths, so it collides with nothing",
			leases: []Job{owner("a", StateRunning, "internal/ledger"), {ID: "scout", State: StateRunning, ReadOnly: true}},
		},
		{
			// The pair is reported once, and the ids read in ledger order so a reader can
			// find both rows in the table they are looking at.
			name: "three leases claiming one tree are three pairs, each named once",
			leases: []Job{
				owner("a", StateRunning, "internal"),
				owner("b", StateRunning, "internal/ledger"),
				owner("c", StateDeclared, "internal/handler"),
			},
			want: []JobOverlap{
				{JobA: "a", JobB: "b", PathsA: []string{"internal"}, PathsB: []string{"internal/ledger"}},
				{JobA: "a", JobB: "c", PathsA: []string{"internal"}, PathsB: []string{"internal/handler"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, jobOverlaps(tt.leases))
		})
	}
}

// A finished lease is not competing for anything. The skill has a worker RELEASE its
// paths when it stops editing, so reporting a pass, a fail, or a no-return as a
// collision would make the surface loudest exactly as the plan winds down.
func TestJobOverlapsSkipsTerminalJobs(t *testing.T) {
	t.Parallel()

	live := owner("live", StateRunning, "internal/ledger")
	for _, state := range []JobState{StatePass, StateFail, StateNoReturn} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			done := owner("done", state, "internal/ledger")
			assert.Empty(t, jobOverlaps([]Job{live, done}))
			assert.Empty(t, jobOverlaps([]Job{done, live}), "whichever order the rows sit in")
		})
	}
}

func TestNewJobListDerivesOverlapsFromTheRows(t *testing.T) {
	t.Parallel()

	leases := []Job{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledger")}
	report := NewJobList(leases)
	assert.Equal(t, leases, report.Jobs, "the rows are served exactly as they were recorded")
	assert.Len(t, report.Overlaps, 1)

	// Derived on read: the same rows with one of them finished report nothing, and no
	// row had to be rewritten for that to happen.
	leases[1].State = StatePass
	assert.Empty(t, NewJobList(leases).Overlaps)
}

// The empty case is settled in the constructor rather than at each read door, so the MCP
// tool and the console's route cannot disagree about it: an unwritten ledger serves an
// empty list, never null, whichever door served it.
func TestNewJobListNormalizesTheEmptyLedger(t *testing.T) {
	t.Parallel()

	report := NewJobList(nil)
	assert.NotNil(t, report.Jobs, "a workspace where nobody has declared a lease yet is empty, not broken")
	assert.Empty(t, report.Jobs)
	assert.Empty(t, report.Overlaps)
}

// Clone is what makes a row handed out of a store safe to keep, so a slice field it
// forgets aliases the store's own array. Every field here carries SPARE CAPACITY, which is
// the shape the ledger actually stores (RecordUnattributedWrite sizes its slice for one
// more than it fills): an append then lands in place, and the damage is invisible to the
// appender: it shows up as the NEXT reader's append overwriting the first one's row. So
// this asserts across two clones rather than back at the original, which is the only form
// of the check that can fail.
func TestJobCloneCopiesEverySliceField(t *testing.T) {
	t.Parallel()

	orig := Job{
		ID:              "a",
		WritePaths:      append(make([]string, 0, 4), "types/"),
		DenyPaths:       append(make([]string, 0, 4), "gen/"),
		DependsOn:       append(make([]string, 0, 4), "b"),
		CompletionGates: append(make([]CompletionGate, 0, 4), CompletionGate{ID: "unit", Check: LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"check"}}),
		GateAttempts:    append(make([]JobGateAttempt, 0, 4), JobGateAttempt{GateID: "unit", Attempt: JobAttempt{Ref: "out1"}}),
		Releases:        append(make([]JobRelease, 0, 4), JobRelease{Path: "types/x.go"}),
		Unattributed:    append(make([]JobUnattributedWrite, 0, 4), JobUnattributedWrite{Path: "types/y.go"}),
	}

	first, second := orig.Clone(), orig.Clone()
	first.WritePaths = append(first.WritePaths, "first/")
	first.DenyPaths = append(first.DenyPaths, "first/")
	first.DependsOn = append(first.DependsOn, "first")
	first.CompletionGates[0].DependsOn = append(first.CompletionGates[0].DependsOn, "first")
	first.GateAttempts = append(first.GateAttempts, JobGateAttempt{GateID: "first"})
	first.Releases = append(first.Releases, JobRelease{Path: "first/z.go"})
	first.Unattributed = append(first.Unattributed, JobUnattributedWrite{Path: "first/z.go"})

	second.WritePaths = append(second.WritePaths, "second/")
	second.DenyPaths = append(second.DenyPaths, "second/")
	second.DependsOn = append(second.DependsOn, "second")
	second.CompletionGates[0].DependsOn = append(second.CompletionGates[0].DependsOn, "second")
	second.GateAttempts = append(second.GateAttempts, JobGateAttempt{GateID: "second"})
	second.Releases = append(second.Releases, JobRelease{Path: "second/z.go"})
	second.Unattributed = append(second.Unattributed, JobUnattributedWrite{Path: "second/z.go"})

	assert.Equal(t, []string{"types/", "first/"}, first.WritePaths)
	assert.Equal(t, []string{"gen/", "first/"}, first.DenyPaths)
	assert.Equal(t, []string{"b", "first"}, first.DependsOn)
	assert.Equal(t, []string{"check", "first"}, first.CompletionGates[0].DependsOn)
	assert.Equal(t, []JobGateAttempt{{GateID: "unit", Attempt: JobAttempt{Ref: "out1"}}, {GateID: "first"}}, first.GateAttempts)
	assert.Equal(t, []JobRelease{{Path: "types/x.go"}, {Path: "first/z.go"}}, first.Releases)
	assert.Equal(t, []JobUnattributedWrite{{Path: "types/y.go"}, {Path: "first/z.go"}}, first.Unattributed)

	// The original is the store's row and nobody appended through it, so it must still
	// hold exactly what it held.
	assert.Equal(t, []string{"types/"}, orig.WritePaths)
	assert.Equal(t, []string{"check"}, orig.CompletionGates[0].DependsOn)
	assert.Equal(t, []JobUnattributedWrite{{Path: "types/y.go"}}, orig.Unattributed)
}

// slices.Clone preserves nil, which is what keeps a row that stored null from coming back
// as [] through the JSON door.
func TestJobCloneKeepsNilSlicesNil(t *testing.T) {
	t.Parallel()

	c := Job{ID: "a"}.Clone()
	assert.Nil(t, c.Unattributed)
	assert.Nil(t, c.Releases)
	assert.Nil(t, c.CompletionGates)
	assert.Nil(t, c.GateAttempts)
}

func TestDeclarationRejectsCyclicCompletionGates(t *testing.T) {
	t.Parallel()

	err := (Declaration{
		ID: "gated",
		CompletionGates: []CompletionGate{
			{ID: "unit", Check: LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"publish"}},
			{ID: "publish", Check: LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"unit"}},
		},
	}).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dependencies contain a cycle")
}

// The parse is what makes a check comparable with a stored run, so the shapes it refuses
// are the point: a flag read as a positional binds evidence to a target nobody ran.
func TestParseLeaseCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		in    string
		want  LeaseCheck
		wants string
	}{
		{name: "target and project", in: "test internal/ledger", want: LeaseCheck{Target: "test", Project: "internal/ledger"}},
		{name: "the project defaults to the root", in: "ci", want: LeaseCheck{Target: "ci", Project: "."}},
		{
			name: "args ride past the separator",
			in:   "go::go-test . -- -run Ledger",
			want: LeaseCheck{Target: "go::go-test", Project: ".", Args: []string{"-run", "Ledger"}},
		},
		{name: "a flag before the separator", in: "-o json test .", wants: "carries the flag -o"},
		{name: "a third word", in: "test a b", wants: "carries 3 words"},
		{name: "nothing at all", in: "  ", wants: "names no target"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseLeaseCheck(tt.in)
			if tt.wants != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wants)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// The rendered line is what the guard and the console still read, so it has to survive a
// round trip through the record the store keeps.
func TestLeaseCheckRoundTripsThroughItsRenderedLine(t *testing.T) {
	t.Parallel()

	want := LeaseCheck{Target: "go::go-test", Project: ".", Args: []string{"-run", "Ledger"}}
	assert.Equal(t, "magus run go::go-test . -- -run Ledger", want.String())

	got, err := ParseLeaseRunLine(want.String())
	require.NoError(t, err)
	assert.Equal(t, want, got)

	_, err = ParseLeaseRunLine("magus affected ci --no-default-charms")
	require.Error(t, err, "a set of runs is not one run, so no ref can be bound to it")
	assert.Contains(t, err.Error(), "is not one")
}

func dependent(id string, state JobState, dependsOn string, paths ...string) Job {
	return Job{ID: id, State: state, WritePaths: paths, DependsOn: []string{dependsOn}}
}

func TestJobOverlapsOmitsADependentThatIsNotReady(t *testing.T) {
	t.Parallel()

	for _, state := range []JobState{StateDeclared, StateRunning, StateExited} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			rows := []Job{owner("dep", state, "internal/job"), dependent("waiter", StateDeclared, "dep", "internal/job")}
			report := NewJobList(rows)
			assert.Empty(t, report.Overlaps, "a job waiting on dep claims nothing dep could collide with")
			assert.Equal(t, []JobBlock{{Job: "waiter", On: "dep", State: state}}, report.Blocked)
		})
	}

	t.Run("a third live job is not shielded by the waiter", func(t *testing.T) {
		t.Parallel()

		rows := []Job{
			owner("dep", StateRunning, "types"),
			dependent("waiter", StateDeclared, "dep", "internal/job"),
			owner("other", StateRunning, "internal/job"),
		}
		assert.Empty(t, NewJobList(rows).Overlaps)
	})
}

func TestJobOverlapsReportsADependentOnceEveryDependencyPasses(t *testing.T) {
	t.Parallel()

	rows := []Job{
		owner("dep", StateRunning, "types"),
		{ID: "waiter", State: StateDeclared, WritePaths: []string{"internal/job"}, DependsOn: []string{"dep", "dep2"}},
		owner("dep2", StatePass),
		owner("other", StateRunning, "internal/job"),
	}
	assert.Empty(t, NewJobList(rows).Overlaps, "one dependency still running keeps the waiter out")

	rows[0].State = StatePass
	report := NewJobList(rows)
	assert.Equal(t, []JobOverlap{{JobA: "waiter", JobB: "other", PathsA: []string{"internal/job"}, PathsB: []string{"internal/job"}}}, report.Overlaps)
	assert.Empty(t, report.Blocked)
}

func TestJobListNamesWhyAJobOwnsNothing(t *testing.T) {
	t.Parallel()

	for _, state := range []JobState{StateFail, StateNoReturn} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			rows := []Job{owner("dep", state), dependent("waiter", StateDeclared, "dep", "internal/job"), owner("other", StateRunning, "internal/job")}
			report := NewJobList(rows)
			assert.Empty(t, report.Overlaps)
			assert.Equal(t, []JobBlock{{Job: "waiter", On: "dep", State: state}}, report.Blocked)
		})
	}

	t.Run("an undeclared dependency", func(t *testing.T) {
		t.Parallel()

		report := NewJobList([]Job{dependent("waiter", StateDeclared, "gone", "internal/job"), owner("other", StateRunning, "internal/job")})
		assert.Empty(t, report.Overlaps)
		assert.Equal(t, []JobBlock{{Job: "waiter", On: "gone"}}, report.Blocked)
	})

	t.Run("a terminal dependent is not reported", func(t *testing.T) {
		t.Parallel()

		assert.Empty(t, NewJobList([]Job{owner("dep", StateFail), dependent("waiter", StateNoReturn, "dep")}).Blocked)
	})

	t.Run("json names the reason", func(t *testing.T) {
		t.Parallel()

		got, err := json.Marshal(NewJobList([]Job{owner("dep", StateFail), dependent("waiter", StateDeclared, "dep")}))
		require.NoError(t, err)
		assert.Contains(t, string(got), `"blocked":[{"job":"waiter","on":"dep","state":"fail"}]`)
	})
}
