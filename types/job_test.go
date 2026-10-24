package types

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidJobID pins the rule every lease channel shares. The marker scanner is not the
// only producer: whatever stamps a Job has to agree with this, or internal/trail's
// redaction exemption starts covering strings nobody checked.
func TestValidJobID(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		id   string
		want bool
	}{
		"plain":               {"mgs1021", true},
		"every separator":     {"a.b-1/2", true},
		"branch shaped":       {"feat/spawn-capture", true},
		"uppercase":           {"MGS1021", false},
		"colon":               {"a:b", false},
		"underscore":          {"a_b", false},
		"double dot":          {"a..b", false},
		"leading slash":       {"/a", false},
		"trailing slash":      {"a/", false},
		"doubled slash":       {"a//b", false},
		"segment dot start":   {"a/.b", false},
		"segment dash start":  {"a/-b", false},
		"leading dash":        {"-a", false},
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
		{
			// Listed rather than dropped: the file is shared, and "disjoint" is what tells the
			// reader the pair is an integration order and not a wait.
			name:   "different declarations of one file are a disjoint pair",
			leases: []Job{owner("a", StateRunning, "run.go#executeStages"), owner("b", StateRunning, "run.go#RunCI", "docs/x.md")},
			want: []JobOverlap{
				{JobA: "a", JobB: "b", PathsA: []string{"run.go#executeStages"}, PathsB: []string{"run.go#RunCI"}, Claims: ClaimsDisjoint},
			},
		},
		{
			name:   "one declaration claimed twice is shared",
			leases: []Job{owner("a", StateRunning, "run.go#RunCI"), owner("b", StateRunning, "run.go# RunCI ")},
			want: []JobOverlap{
				{JobA: "a", JobB: "b", PathsA: []string{"run.go#RunCI"}, PathsB: []string{"run.go# RunCI "}, Claims: ClaimsShared},
			},
		},
		{
			name:   "a declaration inside a directory claimed whole is shared",
			leases: []Job{owner("a", StateRunning, "internal/job"), owner("b", StateRunning, "internal/job/store.go#digest")},
			want: []JobOverlap{
				{JobA: "a", JobB: "b", PathsA: []string{"internal/job"}, PathsB: []string{"internal/job/store.go#digest"}, Claims: ClaimsShared},
			},
		},
		{
			name:   "a disjoint file beside a shared one makes the pair shared",
			leases: []Job{owner("a", StateRunning, "run.go#A", "b.go"), owner("b", StateRunning, "run.go#B", "b.go")},
			want: []JobOverlap{
				{JobA: "a", JobB: "b", PathsA: []string{"run.go#A", "b.go"}, PathsB: []string{"run.go#B", "b.go"}, Claims: ClaimsShared},
			},
		},
		{
			name:   "declarations of different files do not meet at all",
			leases: []Job{owner("a", StateRunning, "run.go#A"), owner("b", StateRunning, "order.go#A")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, jobOverlaps(tt.leases))
		})
	}
}

func TestPathsIntersectReadsTheClaimGrammar(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"run.go#A", "run.go#B", false},
		{"run.go#A", "run.go#A", true},
		{"run.go#A", "run.go", true},
		{"run.go#A", ".", true},
		{"run.go#A", "*.go", true},
		{"run.go#A", "order.go#A", false},
		{"pkg/run.go#A", "pkg", true},
		{"run.go#A", "", false},
	} {
		assert.Equal(t, tc.want, PathsIntersect(tc.a, tc.b), "%q and %q", tc.a, tc.b)
		assert.Equal(t, tc.want, PathsIntersect(tc.b, tc.a), "%q and %q, reversed", tc.b, tc.a)
	}
	assert.Equal(t, "pkg/run.go", LiteralPrefix("pkg/run.go#A"), "a declaration is not a path segment")
}

func TestPathsIntersectMatchesALiteralAgainstAGlobPrecisely(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"internal/guard/guard.go", "internal/guard/*_test.go", false},
		{"internal/guard/guard_test.go", "internal/guard/*_test.go", true},
		{"internal/guard/guard.go#A", "internal/guard/*.go", true},
		{"internal/guard", "internal/guard/*_test.go", true},
		{"internal", "internal/guard/*_test.go", true},
		{"internal/guard/sub/x.go", "internal/guard/*.go", false},
		{"internal/guard/sub/x.go", "internal/guard/**/*.go", true},
		{"a/b/c", "a/b/*/d.go", true},
		{"docs/x.md", "internal/**/*.go", false},
		{"docs/x.md", "**/*.go", true},
		{"pkg/run.go", "*.go", false},
		{"internal/a/x.go", "internal/{a,b}/x.go", true},
		{"internal/c/x.go", "internal/{a,b}/x.go", false},
		{"internal/c/x.go", "internal/{a/b,c}/x.go", true},
		{"internal/guard/guard.go", "internal/guard/*.go", true},
		{"internal/guard/*.go", "internal/*/*_test.go", true},
	} {
		assert.Equal(t, tc.want, PathsIntersect(tc.a, tc.b), "%q and %q", tc.a, tc.b)
		assert.Equal(t, tc.want, PathsIntersect(tc.b, tc.a), "%q and %q, reversed", tc.b, tc.a)
	}
}

// A finished lease is not competing for anything. The skill has a worker RELEASE its
// paths when it stops editing, so reporting a pass, a fail, or a no-return as a
// collision would make the report loudest exactly as the plan winds down.
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

// An exited holder returned, so its write paths collide with nobody's while it waits to be
// graded.
func TestJobOverlapsSkipsAnExitedJob(t *testing.T) {
	t.Parallel()

	live := owner("live", StateRunning, "internal/ledger")
	exited := owner("exited", StateExited, "internal/ledger")
	assert.Empty(t, jobOverlaps([]Job{live, exited}))
	assert.Empty(t, jobOverlaps([]Job{exited, live}), "whichever order the rows sit in")
	assert.True(t, StateExited.Live(), "an exited job stays live until it is graded")
	assert.False(t, StateExited.Editing())
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
		ID:           "a",
		WritePaths:   append(make([]string, 0, 4), "types/"),
		DenyPaths:    append(make([]string, 0, 4), "gen/"),
		DependsOn:    append(make([]string, 0, 4), "b"),
		Breaks:       append(make([]string, 0, 4), "--old-flag"),
		Goals:        append(make([]Goal, 0, 4), Goal{ID: "unit", Check: LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"check"}}),
		GateAttempts: append(make([]JobGateAttempt, 0, 4), JobGateAttempt{GateID: "unit", Attempt: JobAttempt{Ref: "out1"}}),
		Releases:     append(make([]JobRelease, 0, 4), JobRelease{Path: "types/x.go"}),
		Unattributed: append(make([]JobUnattributedWrite, 0, 4), JobUnattributedWrite{Path: "types/y.go"}),
	}

	first, second := orig.Clone(), orig.Clone()
	first.WritePaths = append(first.WritePaths, "first/")
	first.DenyPaths = append(first.DenyPaths, "first/")
	first.DependsOn = append(first.DependsOn, "first")
	first.Breaks = append(first.Breaks, "first")
	first.Goals[0].DependsOn = append(first.Goals[0].DependsOn, "first")
	first.GateAttempts = append(first.GateAttempts, JobGateAttempt{GateID: "first"})
	first.Releases = append(first.Releases, JobRelease{Path: "first/z.go"})
	first.Unattributed = append(first.Unattributed, JobUnattributedWrite{Path: "first/z.go"})

	second.WritePaths = append(second.WritePaths, "second/")
	second.DenyPaths = append(second.DenyPaths, "second/")
	second.DependsOn = append(second.DependsOn, "second")
	second.Breaks = append(second.Breaks, "second")
	second.Goals[0].DependsOn = append(second.Goals[0].DependsOn, "second")
	second.GateAttempts = append(second.GateAttempts, JobGateAttempt{GateID: "second"})
	second.Releases = append(second.Releases, JobRelease{Path: "second/z.go"})
	second.Unattributed = append(second.Unattributed, JobUnattributedWrite{Path: "second/z.go"})

	// Clone resolves each gate's kind and expectation, so the clone's gate carries both.
	assert.Equal(t, Job{
		ID:           "a",
		WritePaths:   []string{"types/", "first/"},
		DenyPaths:    []string{"gen/", "first/"},
		DependsOn:    []string{"b", "first"},
		Breaks:       []string{"--old-flag", "first"},
		Goals:        []Goal{{ID: "unit", Kind: GoalKindCheck, Expect: ExpectPassed, Check: LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"check", "first"}}},
		GateAttempts: []JobGateAttempt{{GateID: "unit", Attempt: JobAttempt{Ref: "out1"}}, {GateID: "first"}},
		Releases:     []JobRelease{{Path: "types/x.go"}, {Path: "first/z.go"}},
		Unattributed: []JobUnattributedWrite{{Path: "types/y.go"}, {Path: "first/z.go"}},
	}, first)

	// The original is the store's row and nobody appended through it, so it must still
	// hold exactly what it held.
	assert.Equal(t, Job{
		ID:           "a",
		WritePaths:   []string{"types/"},
		DenyPaths:    []string{"gen/"},
		DependsOn:    []string{"b"},
		Breaks:       []string{"--old-flag"},
		Goals:        []Goal{{ID: "unit", Check: LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"check"}}},
		GateAttempts: []JobGateAttempt{{GateID: "unit", Attempt: JobAttempt{Ref: "out1"}}},
		Releases:     []JobRelease{{Path: "types/x.go"}},
		Unattributed: []JobUnattributedWrite{{Path: "types/y.go"}},
	}, orig)
}

// A run's args sit one level down, in the attempt and in each goal's attempt, so they
// alias the store's array unless Clone copies them too. Asserted across two clones for the
// reason TestJobCloneCopiesEverySliceField gives.
func TestJobCloneCopiesAttemptArgs(t *testing.T) {
	t.Parallel()

	args := func() []string { return append(make([]string, 0, 4), "-run", "TestOne") }
	orig := Job{
		ID:           "a",
		Attempt:      &JobAttempt{Ref: "out1", ExtraArgs: args()},
		GateAttempts: []JobGateAttempt{{GateID: "unit", Attempt: JobAttempt{Ref: "out2", ExtraArgs: args()}}},
	}

	first, second := orig.Clone(), orig.Clone()
	first.Attempt.ExtraArgs = append(first.Attempt.ExtraArgs, "first")
	first.GateAttempts[0].Attempt.ExtraArgs = append(first.GateAttempts[0].Attempt.ExtraArgs, "first")
	second.Attempt.ExtraArgs = append(second.Attempt.ExtraArgs, "second")
	second.GateAttempts[0].Attempt.ExtraArgs = append(second.GateAttempts[0].Attempt.ExtraArgs, "second")
	first.Attempt.ExtraArgs[0] = "-bench"

	assert.Equal(t, []string{"-bench", "TestOne", "first"}, first.Attempt.ExtraArgs)
	assert.Equal(t, []string{"-run", "TestOne", "first"}, first.GateAttempts[0].Attempt.ExtraArgs)
	assert.Equal(t, []string{"-run", "TestOne"}, orig.Attempt.ExtraArgs)
	assert.Equal(t, []string{"-run", "TestOne"}, orig.GateAttempts[0].Attempt.ExtraArgs)
}

// The previous attempts and the rejection are the next holder's evidence, so a clone handed
// out of the store must not alias the slices one level down. Asserted across two clones for
// the reason TestJobCloneCopiesEverySliceField gives.
func TestJobCloneCopiesPriorAttemptsAndRejection(t *testing.T) {
	t.Parallel()

	refs := func() []string { return append(make([]string, 0, 4), "out1") }
	unmet := func() []GateStatus {
		return append(make([]GateStatus, 0, 4), GateStatus{ID: "unit", Violations: refs()})
	}
	prior := func() PriorAttempt {
		return PriorAttempt{
			N:               1,
			Unmet:           unmet(),
			Violations:      refs(),
			UnresolvedRisks: refs(),
			FailingRefs:     refs(),
			Denials:         append(make([]JobDenial, 0, 4), JobDenial{Rule: "lease-write"}),
		}
	}
	orig := Job{
		ID:        "a",
		Prior:     append(make([]PriorAttempt, 0, 4), prior()),
		Rejection: &JobRejection{Unmet: unmet(), Violations: refs()},
	}

	first, second := orig.Clone(), orig.Clone()
	for i, c := range []Job{first, second} {
		tag := fmt.Sprint(i)
		p := &c.Prior[0]
		p.Unmet[0].Violations = append(p.Unmet[0].Violations, tag)
		p.Violations = append(p.Violations, tag)
		p.UnresolvedRisks = append(p.UnresolvedRisks, tag)
		p.FailingRefs = append(p.FailingRefs, tag)
		p.Denials = append(p.Denials, JobDenial{Rule: tag})
		c.Rejection.Unmet[0].Violations = append(c.Rejection.Unmet[0].Violations, tag)
		c.Rejection.Violations = append(c.Rejection.Violations, tag)
	}
	first.Prior[0].FailingRefs[0] = "first"
	first.Rejection.At = 9

	assert.Equal(t, []string{"first", "0"}, first.Prior[0].FailingRefs)
	wantPrior := second.Prior[0]
	wantPrior.FailingRefs = []string{"out1", "1"}
	wantPrior.Denials = []JobDenial{{Rule: "lease-write"}, {Rule: "1"}}
	assert.Equal(t, wantPrior, second.Prior[0])
	assert.Equal(t, []string{"out1", "1"}, second.Rejection.Unmet[0].Violations)
	assert.Equal(t, Job{ID: "a", Prior: []PriorAttempt{prior()}, Rejection: &JobRejection{Unmet: unmet(), Violations: refs()}}, orig)
}

// A cap on attempts is declared like the rest of the spec, so applying a record that omits
// it clears it, and a negative cap is refused at the door.
func TestDeclarationCarriesMaxAttempts(t *testing.T) {
	t.Parallel()

	require.NoError(t, Declaration{ID: "worker", MaxAttempts: 3}.Validate())
	assert.ErrorContains(t, Declaration{ID: "worker", MaxAttempts: -1}.Validate(), "max_attempts is -1")

	var row Job
	Declaration{ID: "worker", MaxAttempts: 3}.Apply(&row)
	assert.Equal(t, 3, row.MaxAttempts)
	Declaration{ID: "worker"}.ApplySpec(&row)
	assert.Zero(t, row.MaxAttempts)
}

// A weight outside (0,1] is refused, never clamped, and an explicit 0 is out of range rather
// than read as omitted.
func TestDeclarationRefusesAWeightOutOfRange(t *testing.T) {
	t.Parallel()

	for _, w := range []float64{0, -0.5, 1.5, math.NaN()} {
		assert.ErrorContains(t, Declaration{ID: "worker", Weight: &w}.Validate(), "outside (0,1]", "weight %v", w)
	}
	for _, w := range []float64{0.1, 0.5, 1} {
		require.NoError(t, Declaration{ID: "worker", Weight: &w}.Validate(), "weight %v", w)
	}

	var row Job
	w := 0.9
	Declaration{ID: "worker", Weight: &w}.Apply(&row)
	assert.InDelta(t, 0.9, row.Weight, 0)
	row.Paused = 42
	Declaration{ID: "worker"}.ApplySpec(&row)
	assert.Zero(t, row.Weight, "an omitted weight clears the row's, which reads as DefaultJobWeight")
	assert.Equal(t, int64(42), row.Paused, "apply keeps a pause")
}

// The generation and the attempt record are the store's, so a declaration never writes
// them: Apply leaves them where they were.
func TestDeclarationApplyLeavesStoreComputedAttemptsAlone(t *testing.T) {
	t.Parallel()

	row := Job{
		ID:         "worker",
		Generation: 2,
		Attempts:   2,
		Prior:      []PriorAttempt{{N: 1, State: StateFail}},
		Rejection:  &JobRejection{At: 1, ObservedGeneration: 2},
	}
	want := row
	want.Criteria = "again"
	Declaration{ID: "worker", Criteria: "again"}.Apply(&row)
	assert.Equal(t, want, row)
}

func TestJobStaleAtReadsAZeroStampAsUnknown(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name              string
		generation, stamp int
		want              bool
	}{
		{"same generation", 2, 2, false},
		{"an earlier spec", 2, 1, true},
		{"an unstamped record", 2, 0, false},
		{"a row with no generation", 0, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Job{Generation: tc.generation}.StaleAt(tc.stamp))
		})
	}
}

// slices.Clone preserves nil, which is what keeps a row that stored null from coming back
// as [] through the JSON door.
func TestJobCloneKeepsNilSlicesNil(t *testing.T) {
	t.Parallel()

	c := Job{ID: "a"}.Clone()
	assert.Nil(t, c.Unattributed)
	assert.Nil(t, c.Releases)
	assert.Nil(t, c.Goals)
	assert.Nil(t, c.GateAttempts)
}

func TestDeclarationRejectsCyclicGoals(t *testing.T) {
	t.Parallel()

	err := (Declaration{
		ID: "gated",
		Goals: []Goal{
			{ID: "unit", Check: LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"publish"}},
			{ID: "publish", Check: LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"unit"}},
		},
	}).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dependencies contain a cycle")
}

// A path the guard can never match against a workspace-relative one used to be accepted
// and bound nothing, silently.
func TestDeclarationRefusesAPathOutsideTheWorkspace(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		decl Declaration
		want string
	}{
		{Declaration{ID: "j", WritePaths: []string{"~/.claude/plans/**"}}, `write_paths[0] "~/.claude/plans/**" begins with ~`},
		{Declaration{ID: "j", WritePaths: []string{"api/**", "/abs/**"}}, `write_paths[1] "/abs/**" is absolute`},
		{Declaration{ID: "j", DenyPaths: []string{"../wt/**"}}, `deny_paths[0] "../wt/**" climbs out of the workspace`},
		{Declaration{ID: "j", ReadPaths: []string{"docs/../../x"}}, `read_paths[0] "docs/../../x" climbs out`},
	} {
		err := tt.decl.Validate()
		require.Error(t, err, tt.want)
		assert.Contains(t, err.Error(), tt.want)
	}
	assert.NoError(t, Declaration{ID: "j", ReadOnly: true, ReadPaths: []string{"docs/../api/**"}}.Validate(),
		"a path that cleans to one inside the workspace stands")
}

func TestDeclarationValidatesSignatureGoals(t *testing.T) {
	t.Parallel()

	shape := func(id string, symbols ...string) Goal {
		return Goal{ID: id, Kind: GoalKindSymbol, Expect: ExpectSignature, Symbols: symbols}
	}
	for _, tc := range []struct {
		name  string
		goals []Goal
		want  string
	}{
		{"one symbol, signature left for the fork to record", []Goal{shape("a", "Api")}, ""},
		{"one symbol with its signature", []Goal{{ID: "a", Kind: GoalKindSymbol, Expect: ExpectSignature, Symbols: []string{"Api"}, Signature: "func Api()"}}, ""},
		{"two symbols", []Goal{shape("a", "Api", "Old")}, "expects the signature of 2 symbols"},
		{"no symbol", []Goal{shape("a", " ")}, "names nothing to examine"},
		{"a signature on another expectation", []Goal{{ID: "a", Kind: GoalKindSymbol, Expect: ExpectPresent, Symbols: []string{"Api"}, Signature: "func Api()"}},
			`expects "present" and carries a signature`},
		{"two signatures for one symbol", []Goal{shape("a", "Api"), shape("b", "Api")}, `goals "a" and "b" both expect a signature of "Api"`},
		{"signatures for two symbols", []Goal{shape("a", "Api"), shape("b", "Old")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := Declaration{ID: "shaped", Goals: tc.goals}.Validate()
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	assert.Contains(t, GoalExpects(), ExpectSignature)
}

// entries was added without moving JobSchemaVersion, so a schema-11 magus that predates it
// reads such a row, acts on the rest, and writes entries back untouched.
func TestAnOlderReaderKeepsEntries(t *testing.T) {
	t.Parallel()

	type olderJob struct {
		Schema
		ID    string   `json:"id"`
		State JobState `json:"state"`
	}
	in := Job{
		Schema:  Schema{Version: JobSchemaVersion},
		ID:      "worker",
		State:   StateRunning,
		Entries: []JobEntry{{Path: "a.go", By: Origin{User: "eli"}, At: 1, Consumed: 2}},
	}
	raw, err := json.Marshal(in)
	require.NoError(t, err)

	var old olderJob
	require.NoError(t, json.Unmarshal(raw, &old))
	old.State = StatePass
	rewritten, err := json.Marshal(old)
	require.NoError(t, err)

	var back Job
	require.NoError(t, json.Unmarshal(rewritten, &back))
	// The stamp an older reader sees is one it accepts, and it carries the entries through.
	assert.Equal(t, Job{
		Schema:  Schema{Version: JobSchemaVersion},
		ID:      "worker",
		State:   StatePass,
		Entries: in.Entries,
	}, back)
}

func TestDeclarationRefusesABlankBreaksEntry(t *testing.T) {
	t.Parallel()

	require.NoError(t, Declaration{ID: "worker", Breaks: []string{"--old-flag", "api.Open"}}.Validate())
	err := Declaration{ID: "worker", Breaks: []string{"--old-flag", "  "}}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "breaks[1] is blank")
}

// The record is the whole spec, so applying one that omits breaks clears them: a job
// keeps the right to break something only while its declaration says so.
func TestDeclarationApplyCopiesBreaksAndClearsAnOmittedOne(t *testing.T) {
	t.Parallel()

	var row Job
	Declaration{ID: "worker", Breaks: []string{" --old-flag ", "", "api.Open"}}.Apply(&row)
	assert.Equal(t, []string{"--old-flag", "api.Open"}, row.Breaks)

	Declaration{ID: "worker"}.ApplySpec(&row)
	assert.Empty(t, row.Breaks)
}

func TestDeclarationIntegratesNamesMergeableBranchesOnce(t *testing.T) {
	t.Parallel()

	ok := Declaration{ID: "integrate", Integrates: []string{"done-a", "feature/done-b"}}
	require.NoError(t, ok.Validate())
	var row Job
	ok.Apply(&row)
	assert.True(t, row.Integrating())
	assert.Equal(t, []string{"done-a", "feature/done-b"}, row.Clone().Integrates)

	for branches, want := range map[string]string{
		"--force":       `integrates[0] "--force" is not a branch name a merge can take`,
		"a..b":          `integrates[0] "a..b" is not a branch name a merge can take`,
		"done done":     `integrates[0] "done done" is not a branch name a merge can take`,
		"done-a,done-a": `integrates[1] "done-a" is named twice`,
		"main.lock":     `integrates[0] "main.lock" is not a branch name a merge can take`,
	} {
		err := Declaration{ID: "integrate", Integrates: strings.Split(branches, ",")}.Validate()
		require.Error(t, err, branches)
		assert.Contains(t, err.Error(), want, branches)
	}
	err := Declaration{ID: "integrate", Integrates: []string{"done-a"}, ReadOnly: true}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "an integration job merges branches, so it writes")
	assert.False(t, Job{ID: "worker"}.Integrating())
}

func TestDeclarationCarryingEnterDeclaresNothingElse(t *testing.T) {
	t.Parallel()

	schema := Schema{Version: JobSchemaVersion}
	require.NoError(t, Declaration{Schema: schema, ID: "worker", Enter: "a.go"}.Validate())
	err := Declaration{Schema: schema, ID: "worker", Enter: "a.go", WritePaths: []string{"**"}}.Validate()
	assert.ErrorContains(t, err, "carries only schema_version, id and enter")
}

func TestSameLeaseCheckComparesEveryField(t *testing.T) {
	t.Parallel()

	base := LeaseCheck{Target: "test", Script: "probe.buzz", Project: "p", Args: []string{"-run", "A"}, NoDefaultCharms: true}
	assert.True(t, SameLeaseCheck(&base, &base))
	same := base
	same.Args = []string{"-run", "A"}
	assert.True(t, SameLeaseCheck(&base, &same), "equal by value, not by identity")

	for i := range reflect.TypeFor[LeaseCheck]().NumField() {
		other := base
		field := reflect.ValueOf(&other).Elem().Field(i)
		switch field.Kind() {
		case reflect.String:
			field.SetString(field.String() + "x")
		case reflect.Bool:
			field.SetBool(!field.Bool())
		case reflect.Slice:
			field.Set(reflect.ValueOf([]string{"-run", "B"}))
		default:
			t.Fatalf("LeaseCheck.%s has kind %s, which this test does not know how to vary", reflect.TypeFor[LeaseCheck]().Field(i).Name, field.Kind())
		}
		assert.False(t, SameLeaseCheck(&base, &other), "a differing %s is a different check", reflect.TypeFor[LeaseCheck]().Field(i).Name)
	}
}

func TestSameLeaseCheckTreatsNilAsNoCheck(t *testing.T) {
	t.Parallel()

	declared := &LeaseCheck{Target: "test", Project: "."}
	assert.True(t, SameLeaseCheck(nil, nil))
	assert.False(t, SameLeaseCheck(nil, declared))
	assert.False(t, SameLeaseCheck(declared, nil))
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
		{
			name: "the charm flag is read, not refused",
			in:   "lint . --no-default-charms",
			want: LeaseCheck{Target: "lint", Project: ".", NoDefaultCharms: true},
		},
		{
			name: "the charm flag may lead",
			in:   "--no-default-charms generate .",
			want: LeaseCheck{Target: "generate", Project: ".", NoDefaultCharms: true},
		},
		{name: "another magus flag stays refused beside the charm flag", in: "lint . --no-default-charms -o json", wants: "carries the flag -o"},
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

func TestScriptCheckRendersAsARecordedRunAndNamesOneSubject(t *testing.T) {
	t.Parallel()

	want := LeaseCheck{Script: "probes/key.buzz", Args: []string{"--deep"}}
	assert.Equal(t, "magus buzz --record probes/key.buzz -- --deep", want.String())
	got, err := ParseLeaseRunLine(want.String())
	require.NoError(t, err)
	assert.Equal(t, want, got)

	gate := Goal{ID: "probe", Kind: GoalKindCheck, Check: LeaseCheck{Script: "probes/key.buzz"}}.Resolve()
	require.NoError(t, gate.Validate())

	both := gate
	both.Check.Target = "test"
	require.ErrorContains(t, both.Validate(), "a check names one")

	scoped := gate
	scoped.Check.Project = "cmd/magus"
	require.ErrorContains(t, scoped.Validate(), "belongs to the workspace")

	tested := gate
	tested.Check.Test = true
	require.NoError(t, tested.Validate())
	assert.Equal(t, "magus buzz -t probes/key.buzz", tested.Check.String())
}

func TestTestCheckIsAScriptChecksAlone(t *testing.T) {
	t.Parallel()

	target := Goal{ID: "unit", Kind: GoalKindCheck, Check: LeaseCheck{Target: "test", Project: ".", Test: true}}.Resolve()
	require.ErrorContains(t, target.Validate(), `check target "test" sets test`)

	rec := Declaration{Schema: Schema{Version: JobSchemaVersion}, ID: "fix-parser", Check: &LeaseCheck{Target: "test", Test: true}}
	_, _, ps := rec.check()
	require.Len(t, ps, 1)
	assert.Equal(t, "check.test", ps[0].Field)

	rec.Check = &LeaseCheck{Script: "probes/key.buzz", Test: true}
	c, ok, ps := rec.check()
	require.Empty(t, ps)
	assert.True(t, ok)
	assert.True(t, c.Test, "the record's test reaches the row's check")
}

// An exited dependency is done editing, so nobody collecting it leaves its dependent
// queued forever: the clock starts at the exit, and only a declared or running one holds it.
func TestJobIdleSinceStartsAtAnExitedDependency(t *testing.T) {
	t.Parallel()

	row := Job{ID: "next", State: StateDeclared, Updated: 100, DependsOn: []string{"first"}}
	exited := Job{ID: "first", State: StateExited, Updated: 300}
	since, waiting := JobIdleSince([]Job{exited, row}, row)
	assert.False(t, waiting)
	assert.Equal(t, int64(300), since)
	assert.True(t, JobStaleAt([]Job{exited, row}, row, 300+3600, time.Hour))

	for _, state := range []JobState{StateRunning, StateDeclared} {
		dep := Job{ID: "first", State: state, Updated: 300}
		_, waiting = JobIdleSince([]Job{dep, row}, row)
		assert.True(t, waiting, "a %s dependency still holds the clock", state)
	}
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

func TestJobListFlagsAQueuedJobStaleOnlyOnceItsDependenciesEnd(t *testing.T) {
	t.Parallel()

	const now, hour = int64(100_000), int64(3600)
	rows := []Job{
		{ID: "dep", State: StateRunning, Registered: now, Updated: now},
		{ID: "queued", State: StateDeclared, DependsOn: []string{"dep"}, Updated: now - 3*hour},
		{ID: "lone", State: StateDeclared, Updated: now - 3*hour},
	}
	assert.Equal(t, []string{"lone"}, NewJobList(rows).Flag(now, 2*time.Hour).Stale, "a live dependency keeps its waiter queued")

	rows[0].State, rows[0].Updated = StatePass, now-hour
	assert.Equal(t, []string{"lone"}, NewJobList(rows).Flag(now, 2*time.Hour).Stale, "the waiter's clock starts at its dependency's end")
	assert.Equal(t, []string{"queued", "lone"}, NewJobList(rows).Flag(now+2*hour, 2*time.Hour).Stale)
}

// JobAncestors walks nearest first, stops at a parent the rows do not carry and at a
// cycle, and allocates only the chain it returns: every list asks it of every live row,
// and a per-call index of the whole store made one list quadratic in allocations.
func TestJobAncestors(t *testing.T) {
	rows := []Job{
		{ID: "root"},
		{ID: "mid", Parent: "root"},
		{ID: "leaf", Parent: "mid"},
		{ID: "orphan", Parent: "missing"},
		{ID: "a", Parent: "b"},
		{ID: "b", Parent: "a"},
		{ID: "into", Parent: "a"},
	}
	ids := func(id string) []string {
		var out []string
		for _, a := range JobAncestors(rows, id) {
			out = append(out, a.ID)
		}
		return out
	}
	assert.Equal(t, []string{"mid", "root"}, ids("leaf"))
	assert.Empty(t, ids("root"))
	assert.Empty(t, ids("orphan"), "a parent the rows do not carry ends the walk")
	assert.Equal(t, []string{"b"}, ids("a"), "a cycle back to the start ends the walk")
	assert.Equal(t, []string{"a", "b"}, ids("into"), "a cycle above the start ends the walk")

	for i := range 1000 {
		rows = append(rows, Job{ID: fmt.Sprintf("filler-%d", i)})
	}
	allocs := testing.AllocsPerRun(10, func() { JobAncestors(rows, "leaf") })
	assert.LessOrEqual(t, allocs, 4.0, "only the returned chain allocates, whatever the store's size")
}

// A footprint that is not known still says so on the wire: footprint_known is never
// omitted, so a reader cannot take a missing footprint for an empty one.
func TestFootprintsCrossJSON(t *testing.T) {
	t.Parallel()

	status, err := json.Marshal(JobStatus{Job: "a", FootprintReason: "git does not report changed regions (RegionReporter)"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"job":"a","verified":false,"footprint_known":false,"footprint_reason":"git does not report changed regions (RegionReporter)","lines_added":0,"lines_deleted":0,"symbol_delta_known":false}`, string(status))

	status, err = json.Marshal(JobStatus{Job: "a", FootprintKnown: true, Footprint: []RegionChange{
		{File: FileChange{Path: "a.go"}, Side: RegionNew, Lines: [2]int{1, 4}, Declaration: "func X() {", Driver: "golang"},
	}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"job":"a","verified":false,"footprint_known":true,"footprint":[`+
		`{"file":{"path":"a.go"},"side":"new","lines":[1,4],"declaration":"func X() {","driver":"golang"}],"lines_added":0,"lines_deleted":0,"symbol_delta_known":false}`, string(status))

	status, err = json.Marshal(JobStatus{Job: "a", ObservedGeneration: 2, PriorGeneration: 1})
	require.NoError(t, err)
	assert.JSONEq(t, `{"job":"a","verified":false,"footprint_known":false,"lines_added":0,"lines_deleted":0,"symbol_delta_known":false,`+
		`"observed_generation":2,"prior_generation":1}`, string(status))

	overlap, err := json.Marshal(JobOverlap{JobA: "a", JobB: "b", PathsA: []string{"x"}, PathsB: []string{"x"},
		Footprint: &JobOverlapFootprint{Verdict: FootprintShared, Shared: []string{"x/a.go#func X() {"}}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"job_a":"a","job_b":"b","paths_a":["x"],"paths_b":["x"],`+
		`"footprint":{"verdict":"shared","shared":["x/a.go#func X() {"]}}`, string(overlap))
}

func TestDeclarationProblemsCollectsEveryProblem(t *testing.T) {
	t.Parallel()
	d := Declaration{
		ID:          "bad id",
		Parent:      "bad parent",
		DependsOn:   []string{"ok", "bad dep"},
		State:       "nope",
		Timeout:     "soon",
		MaxAttempts: -1,
		Checkout:    "rel/dir",
		WritePaths:  []string{"a.go", "../b.go"},
		Breaks:      []string{" "},
		Check:       &LeaseCheck{Target: "test", Project: "."},
		Validation:  "magus run test .",
		Waivers: []JobWaiver{
			{Code: JobRecordInvalid, Subject: "x", Reason: "r"},
			{Code: GateNotRun},
		},
		Goals: []Goal{
			{ID: "a", Kind: GoalKindPaths},
			{ID: "a", Kind: GoalKindPaths, Paths: []string{"x"}, DependsOn: []string{"missing"}},
		},
	}

	ps := d.Problems()

	fields := make([]string, len(ps))
	for i, p := range ps {
		fields[i] = fmt.Sprintf("%s %s", string(p.Code), p.Field)
		assert.NotEmptyf(t, p.Message, "%s has a message", fields[i])
		assert.NotEmptyf(t, p.Fix, "%s has a fix", fields[i])
	}
	assert.Equal(t, []string{
		"MGS3037 id",
		"MGS3037 parent",
		"MGS3037 depends_on[1]",
		"MGS3037 state",
		"MGS3037 timeout",
		"MGS3037 max_attempts",
		"MGS3037 checkout",
		"MGS3037 write_paths[1]",
		"MGS3037 validation",
		"MGS3037 breaks[0]",
		"MGS3037 waivers[0].code",
		"MGS3037 waivers[1].subject",
		"MGS3037 waivers[1].reason",
		"MGS3038 goals[0].paths",
		"MGS3038 goals[1].id",
		"MGS3038 goals[1].depends_on[0]",
	}, fields)
	err := d.Validate()
	require.ErrorIs(t, err, JobRecordInvalid)
	require.ErrorIs(t, err, JobGoalInvalid)
}

func TestDeclarationProblemsOfAValidRecordIsEmpty(t *testing.T) {
	t.Parallel()
	d := Declaration{
		ID:         "wave/a",
		WritePaths: []string{"types/job.go"},
		Check:      &LeaseCheck{Target: "go-test", Project: "."},
		Waivers:    []JobWaiver{{Code: GateNotRun, Subject: "lint .", Reason: "the root runs it"}},
		Goals:      []Goal{{ID: "names", Kind: GoalKindSymbol, Symbols: []string{"X"}, DependsOn: []string{PrimaryGoalID}}},
	}

	assert.Empty(t, d.Problems())
	require.NoError(t, d.Validate())
}

func TestDeclarationProblemsReportsEachGoalCycleOnce(t *testing.T) {
	t.Parallel()
	d := Declaration{ID: "a", Goals: []Goal{
		{ID: "x", Kind: GoalKindPaths, Paths: []string{"p"}, DependsOn: []string{"y"}},
		{ID: "y", Kind: GoalKindPaths, Paths: []string{"p"}, DependsOn: []string{"x"}},
		{ID: "z", Kind: GoalKindPaths, Paths: []string{"p"}, DependsOn: []string{"z"}},
	}}

	ps := d.Problems()

	require.Len(t, ps, 2)
	assert.Equal(t, "goals[0].depends_on", ps[1].Field)
	assert.Contains(t, ps[1].Message, `cycle at "x"`)
	assert.Equal(t, "goals[2].depends_on[0]", ps[0].Field)
	assert.Contains(t, ps[0].Message, "cannot depend on itself")
}

func TestDeclarationApplyCopiesWaivers(t *testing.T) {
	t.Parallel()
	waivers := []JobWaiver{{Code: GateNotRun, Subject: "lint .", Reason: "the root runs it"}}
	var row Job

	Declaration{ID: "a", Waivers: waivers}.Apply(&row)
	waivers[0].Reason = "changed"

	assert.Equal(t, "the root runs it", row.Waivers[0].Reason)
}

func TestGoalProblemsNameTheFieldUnderTheGoal(t *testing.T) {
	t.Parallel()
	g := Goal{ID: "g", Kind: GoalKindSymbol, Expect: ExpectSignature, Symbols: []string{"A", "B"}, Paths: []string{"x"}}

	ps := g.problems("goals[3]")

	require.Len(t, ps, 2)
	assert.Equal(t, "goals[3].paths", ps[0].Field)
	assert.Equal(t, "goals[3].symbols", ps[1].Field)
	for _, p := range ps {
		assert.Equal(t, JobGoalInvalid, p.Code)
	}
	require.ErrorIs(t, g.Validate(), JobGoalInvalid)
}

func TestPathWithin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		inner, outer string
		want         bool
	}{
		{"internal/job/a.go", "internal/job/a.go", true},
		{"internal/job/a.go", "internal/job", true},
		{"internal/job/a.go", "internal/jo", false},
		{"internal/job/a.go", "internal/job/*.go", true},
		{"internal/job/a.go", "internal/**", true},
		{"internal/job/a.txt", "internal/job/*.go", false},
		{"internal/job/a.go#Run", "internal/job/a.go", true},
		{"internal/job/a.go#Run", "internal/job/*.go", true},
		{"internal/job/a.go#Run", "internal/job/a.go#Run", true},
		{"internal/job/a.go#Run", "internal/job/a.go#Stop", false},
		{"internal/job/a.go", "internal/job/a.go#Run", false},
		{"internal/job/*.go", "internal/job/a.go#Run", false},
		{"internal/job/*.go", "internal/job", true},
		{"internal/job/*.go", "internal/job/a.go", false},
		{"internal/job/*.go", "internal/**", true},
		{"internal/job/*.go", "internal/**/*.go", true},
		{"internal/*/x/*.go", "internal/**/*.go", true},
		{"internal/job/a.go", "internal/**/*.go", true},
		{"internal/*.go", "internal/**/*.go", true},
		{"internal/job/**", "internal/**/*.go", false},
		{"internal/job/*.txt", "internal/**/*.go", false},
		{"internal/job/*_test.go", "internal/**/*.go", true},
		{"internal/job/*.go", "internal/**/*_test.go", false},
		{"internal/job/*", "internal/**/*.go", false},
		{"internal/job/*.{go,txt}", "internal/**/*.go", false},
		{"internal/job/*.[a-z]", "internal/**/*.go", false},
		{"internal/job/*.go", "internal/**/*", true},
		{".", "internal/**", false},
		{".", "internal/**/*.go", false},
		{".", "internal/job/*.go", false},
		{"internal/**", "internal/job/**", false},
		{"**/*.go", "internal/**", false},
		{"**/*.go", "**", true},
		{"internal/job/*.go", "internal/job/*.go", true},
		{"", "internal/job", false},
		{"internal/job/a.go", "", false},
	} {
		assert.Equalf(t, tc.want, PathWithin(tc.inner, tc.outer), "PathWithin(%q, %q)", tc.inner, tc.outer)
	}
}

func TestParseClaimEvidence(t *testing.T) {
	t.Parallel()

	for because, want := range map[string]ClaimEvidence{
		"refs:hint.VCSAdd":         {Kind: ClaimRefs, Subject: "hint.VCSAdd"},
		"test:cmd/magus/vcs.go":    {Kind: ClaimTest, Subject: "cmd/magus/vcs.go"},
		"output:.:config-generate": {Kind: ClaimOutput, Subject: ".:config-generate"},
		"released":                 {Kind: ClaimReleased},
	} {
		got, err := ParseClaimEvidence(because)
		require.NoError(t, err, because)
		assert.Equal(t, want, got)
		assert.Equal(t, because, got.String(), "the evidence spells back as its because")
	}
	for because, refused := range map[string]string{
		"hunch:x":         "names no proof",
		"refs:":           "names no refs subject",
		"released:a.go":   "released names nothing after it",
		"output:generate": "names no target",
	} {
		_, err := ParseClaimEvidence(because)
		require.Error(t, err, because)
		assert.Contains(t, err.Error(), refused)
	}
}

func TestAClaimRecordDeclaresNothingElse(t *testing.T) {
	t.Parallel()

	rec := Declaration{Schema: Schema{Version: JobSchemaVersion}, ID: "cli-ls-files", Claim: []JobClaimRequest{{Path: "internal/guard/shell.go", Because: "refs:hint.VCSAdd"}}}
	assert.Empty(t, rec.Problems())

	rec.Criteria = "more"
	rec.Claim = append(rec.Claim, JobClaimRequest{Path: "../x.go", Because: "released"}, JobClaimRequest{Path: "internal/**", Because: "maybe"})
	var fields []string
	for _, p := range rec.Problems() {
		fields = append(fields, p.Field)
	}
	assert.Equal(t, []string{"claim", "claim[1].path", "claim[2].path", "claim[2].because"}, fields)
}

func TestDeclarationCarriesMaxClaims(t *testing.T) {
	t.Parallel()

	require.NoError(t, Declaration{ID: "worker", MaxClaims: 4}.Validate())
	assert.ErrorContains(t, Declaration{ID: "worker", MaxClaims: -1}.Validate(), "max_claims is -1")

	var row Job
	Declaration{ID: "worker", MaxClaims: 4}.Apply(&row)
	assert.Equal(t, 4, row.MaxClaims)
	Declaration{ID: "worker"}.ApplySpec(&row)
	assert.Zero(t, row.MaxClaims)
}
