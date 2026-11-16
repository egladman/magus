package types

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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
		"at the length cap":   {strings.Repeat("u", job.MaxIDLen), true},
		"past the length cap": {strings.Repeat("u", job.MaxIDLen+1), false},
		"empty":               {"", false},
		"space":               {"two words", false},
		"punctuation":         {"MGS1021!", false},
		"newline":             {"lease\n", false},
		"non-ascii":           {"lease-é", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, job.ValidID(tc.id))
		})
	}
}

func owner(id string, state job.State, paths ...string) job.Job {
	return job.Job{ID: id, State: state, Paths: job.Paths{Write: paths}}
}

func TestJobOverlaps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		leases []job.Job
		want   []job.Overlap
	}{
		{
			name:   "the same path claimed twice",
			leases: []job.Job{owner("a", job.StateRunning, "internal/ledger"), owner("b", job.StateDeclared, "internal/ledger")},
			want: []job.Overlap{
				{JobA: "a", JobB: "b", PathsA: []string{"internal/ledger"}, PathsB: []string{"internal/ledger"}},
			},
		},
		{
			// The common case a reader most needs told: one lease owns the directory, the
			// other owns a file inside it, and nothing about either row says so.
			name:   "a file inside a claimed directory, and both declarations are named",
			leases: []job.Job{owner("a", job.StateRunning, "internal/ledger"), owner("b", job.StateRunning, "internal/ledger/store.go")},
			want: []job.Overlap{
				{
					JobA: "a", JobB: "b",
					PathsA: []string{"internal/ledger"}, PathsB: []string{"internal/ledger/store.go"},
				},
			},
		},
		{
			name:   "a glob is judged by the directories it names",
			leases: []job.Job{owner("a", job.StateRunning, "console/src/**/*.ts"), owner("b", job.StateRunning, "console/src/console/plan/main.ts")},
			want: []job.Overlap{
				{
					JobA: "a", JobB: "b",
					PathsA: []string{"console/src/**/*.ts"},
					PathsB: []string{"console/src/console/plan/main.ts"},
				},
			},
		},
		{
			name:   "sibling directories are not an overlap",
			leases: []job.Job{owner("a", job.StateRunning, "internal/ledger"), owner("b", job.StateRunning, "internal/ledgerx", "console/")},
		},
		{
			// A prefix that stops mid-segment is a different directory, not a parent.
			name:   "a shared name prefix inside a segment is not containment",
			leases: []job.Job{owner("a", job.StateRunning, "internal/led"), owner("b", job.StateRunning, "internal/ledger/store.go")},
		},
		{
			// A blank entry names nothing. It cleans to ".", and reading THAT as a claim on
			// the whole tree paired the row holding it with every other lease in the plan.
			name:   "a blank declaration claims nothing, not everything",
			leases: []job.Job{owner("a", job.StateRunning, "  ", ""), owner("b", job.StateRunning, "internal/ledger")},
		},
		{
			name:   "a read-only lease declares no paths, so it collides with nothing",
			leases: []job.Job{owner("a", job.StateRunning, "internal/ledger"), {ID: "scout", State: job.StateRunning, ReadOnly: true}},
		},
		{
			// The pair is reported once, and the ids read in ledger order so a reader can
			// find both rows in the table they are looking at.
			name: "three leases claiming one tree are three pairs, each named once",
			leases: []job.Job{
				owner("a", job.StateRunning, "internal"),
				owner("b", job.StateRunning, "internal/ledger"),
				owner("c", job.StateDeclared, "internal/handler"),
			},
			want: []job.Overlap{
				{JobA: "a", JobB: "b", PathsA: []string{"internal"}, PathsB: []string{"internal/ledger"}},
				{JobA: "a", JobB: "c", PathsA: []string{"internal"}, PathsB: []string{"internal/handler"}},
			},
		},
		{
			// Listed rather than dropped: the file is shared, and "disjoint" is what tells the
			// reader the pair is an integration order and not a wait.
			name:   "different declarations of one file are a disjoint pair",
			leases: []job.Job{owner("a", job.StateRunning, "run.go#executeStages"), owner("b", job.StateRunning, "run.go#RunCI", "docs/x.md")},
			want: []job.Overlap{
				{JobA: "a", JobB: "b", PathsA: []string{"run.go#executeStages"}, PathsB: []string{"run.go#RunCI"}, Claims: job.ClaimsDisjoint},
			},
		},
		{
			name:   "one declaration claimed twice is shared",
			leases: []job.Job{owner("a", job.StateRunning, "run.go#RunCI"), owner("b", job.StateRunning, "run.go# RunCI ")},
			want: []job.Overlap{
				{JobA: "a", JobB: "b", PathsA: []string{"run.go#RunCI"}, PathsB: []string{"run.go# RunCI "}, Claims: job.ClaimsShared},
			},
		},
		{
			name:   "a declaration inside a directory claimed whole is shared",
			leases: []job.Job{owner("a", job.StateRunning, "internal/job"), owner("b", job.StateRunning, "internal/job/store.go#digest")},
			want: []job.Overlap{
				{JobA: "a", JobB: "b", PathsA: []string{"internal/job"}, PathsB: []string{"internal/job/store.go#digest"}, Claims: job.ClaimsShared},
			},
		},
		{
			name:   "a disjoint file beside a shared one makes the pair shared",
			leases: []job.Job{owner("a", job.StateRunning, "run.go#A", "b.go"), owner("b", job.StateRunning, "run.go#B", "b.go")},
			want: []job.Overlap{
				{JobA: "a", JobB: "b", PathsA: []string{"run.go#A", "b.go"}, PathsB: []string{"run.go#B", "b.go"}, Claims: job.ClaimsShared},
			},
		},
		{
			name:   "declarations of different files do not meet at all",
			leases: []job.Job{owner("a", job.StateRunning, "run.go#A"), owner("b", job.StateRunning, "order.go#A")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, job.NewList(tt.leases).Overlaps)
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
		assert.Equal(t, tc.want, job.PathsIntersect(tc.a, tc.b), "%q and %q", tc.a, tc.b)
		assert.Equal(t, tc.want, job.PathsIntersect(tc.b, tc.a), "%q and %q, reversed", tc.b, tc.a)
	}
	assert.Equal(t, "pkg/run.go", job.LiteralPrefix("pkg/run.go#A"), "a declaration is not a path segment")
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
		assert.Equal(t, tc.want, job.PathsIntersect(tc.a, tc.b), "%q and %q", tc.a, tc.b)
		assert.Equal(t, tc.want, job.PathsIntersect(tc.b, tc.a), "%q and %q, reversed", tc.b, tc.a)
	}
}

// A finished lease is not competing for anything. The skill has a worker RELEASE its
// paths when it stops editing, so reporting a pass, a fail, or a no-return as a
// collision would make the report loudest exactly as the plan winds down.
func TestJobOverlapsSkipsTerminalJobs(t *testing.T) {
	t.Parallel()

	live := owner("live", job.StateRunning, "internal/ledger")
	for _, state := range []job.State{job.StatePass, job.StateFail, job.StateNoReturn} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			done := owner("done", state, "internal/ledger")
			assert.Empty(t, job.NewList([]job.Job{live, done}).Overlaps)
			assert.Empty(t, job.NewList([]job.Job{done, live}).Overlaps, "whichever order the rows sit in")
		})
	}
}

// TestPathHoldersKeepsEditingRows: a path is held by a declared or running row only. An
// exited row filed its result, so its outputs are anyone's to regenerate.
func TestPathHoldersKeepsEditingRows(t *testing.T) {
	t.Parallel()

	rows := []job.Job{
		owner("declared", job.StateDeclared, "a"),
		owner("running", job.StateRunning, "b"),
		owner("exited", job.StateExited, "c"),
		owner("passed", job.StatePass, "d"),
		owner("unset", "", "e"),
	}
	var ids []string
	for _, r := range job.PathHolders(rows) {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"declared", "running"}, ids)
	assert.Len(t, rows, 5, "the rows passed in are left whole")
}

// An exited holder returned, so its write paths collide with nobody's while it waits to be
// graded.
func TestJobOverlapsSkipsAnExitedJob(t *testing.T) {
	t.Parallel()

	live := owner("live", job.StateRunning, "internal/ledger")
	exited := owner("exited", job.StateExited, "internal/ledger")
	assert.Empty(t, job.NewList([]job.Job{live, exited}).Overlaps)
	assert.Empty(t, job.NewList([]job.Job{exited, live}).Overlaps, "whichever order the rows sit in")
	assert.True(t, job.StateExited.Live(), "an exited job stays live until it is graded")
	assert.False(t, job.StateExited.Editing())
}

func TestNewJobListDerivesOverlapsFromTheRows(t *testing.T) {
	t.Parallel()

	leases := []job.Job{owner("a", job.StateRunning, "internal/ledger"), owner("b", job.StateRunning, "internal/ledger")}
	report := job.NewList(leases)
	assert.Equal(t, leases, report.Jobs, "the rows are served exactly as they were recorded")
	assert.Len(t, report.Overlaps, 1)

	// Derived on read: the same rows with one of them finished report nothing, and no
	// row had to be rewritten for that to happen.
	leases[1].State = job.StatePass
	assert.Empty(t, job.NewList(leases).Overlaps)
}

// The empty case is settled in the constructor rather than at each read door, so the MCP
// tool and the console's route cannot disagree about it: an unwritten ledger serves an
// empty list, never null, whichever door served it.
func TestNewJobListNormalizesTheEmptyLedger(t *testing.T) {
	t.Parallel()

	report := job.NewList(nil)
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

	orig := job.Job{
		ID: "a",
		Paths: job.Paths{
			Write: append(make([]string, 0, 4), "types/"),
			Deny:  append(make([]string, 0, 4), "gen/"),
		},
		DependsOn:    append(make([]string, 0, 4), "b"),
		Breaks:       append(make([]string, 0, 4), "--old-flag"),
		Goals:        append(make([]job.Goal, 0, 4), job.Goal{ID: "unit", Check: job.LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"check"}}),
		GateAttempts: append(make([]job.GateAttempt, 0, 4), job.GateAttempt{GateID: "unit", Attempt: job.CapturedRun{Ref: "out1"}}),
		Releases:     append(make([]job.Release, 0, 4), job.Release{Path: "types/x.go"}),
		Unattributed: append(make([]job.UnattributedWrite, 0, 4), job.UnattributedWrite{Path: "types/y.go"}),
	}

	first, second := orig.Clone(), orig.Clone()
	first.Paths.Write = append(first.Paths.Write, "first/")
	first.Paths.Deny = append(first.Paths.Deny, "first/")
	first.DependsOn = append(first.DependsOn, "first")
	first.Breaks = append(first.Breaks, "first")
	first.Goals[0].DependsOn = append(first.Goals[0].DependsOn, "first")
	first.GateAttempts = append(first.GateAttempts, job.GateAttempt{GateID: "first"})
	first.Releases = append(first.Releases, job.Release{Path: "first/z.go"})
	first.Unattributed = append(first.Unattributed, job.UnattributedWrite{Path: "first/z.go"})

	second.Paths.Write = append(second.Paths.Write, "second/")
	second.Paths.Deny = append(second.Paths.Deny, "second/")
	second.DependsOn = append(second.DependsOn, "second")
	second.Breaks = append(second.Breaks, "second")
	second.Goals[0].DependsOn = append(second.Goals[0].DependsOn, "second")
	second.GateAttempts = append(second.GateAttempts, job.GateAttempt{GateID: "second"})
	second.Releases = append(second.Releases, job.Release{Path: "second/z.go"})
	second.Unattributed = append(second.Unattributed, job.UnattributedWrite{Path: "second/z.go"})

	// Clone resolves each gate's kind and expectation, so the clone's gate carries both.
	assert.Equal(t, job.Job{
		ID: "a",
		Paths: job.Paths{
			Write: []string{"types/", "first/"},
			Deny:  []string{"gen/", "first/"},
		},
		DependsOn:    []string{"b", "first"},
		Breaks:       []string{"--old-flag", "first"},
		Goals:        []job.Goal{{ID: "unit", Kind: job.GoalKindCheck, Expect: job.ExpectPassed, Check: job.LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"check", "first"}}},
		GateAttempts: []job.GateAttempt{{GateID: "unit", Attempt: job.CapturedRun{Ref: "out1"}}, {GateID: "first"}},
		Releases:     []job.Release{{Path: "types/x.go"}, {Path: "first/z.go"}},
		Unattributed: []job.UnattributedWrite{{Path: "types/y.go"}, {Path: "first/z.go"}},
	}, first)

	// The original is the store's row and nobody appended through it, so it must still
	// hold exactly what it held.
	assert.Equal(t, job.Job{
		ID: "a",
		Paths: job.Paths{
			Write: []string{"types/"},
			Deny:  []string{"gen/"},
		},
		DependsOn:    []string{"b"},
		Breaks:       []string{"--old-flag"},
		Goals:        []job.Goal{{ID: "unit", Check: job.LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"check"}}},
		GateAttempts: []job.GateAttempt{{GateID: "unit", Attempt: job.CapturedRun{Ref: "out1"}}},
		Releases:     []job.Release{{Path: "types/x.go"}},
		Unattributed: []job.UnattributedWrite{{Path: "types/y.go"}},
	}, orig)
}

// A run's args sit one level down, in the attempt and in each goal's attempt, so they
// alias the store's array unless Clone copies them too. Asserted across two clones for the
// reason TestJobCloneCopiesEverySliceField gives.
func TestJobCloneCopiesAttemptArgs(t *testing.T) {
	t.Parallel()

	args := func() []string { return append(make([]string, 0, 4), "-run", "TestOne") }
	orig := job.Job{
		ID:           "a",
		Attempt:      &job.CapturedRun{Ref: "out1", ExtraArgs: args()},
		GateAttempts: []job.GateAttempt{{GateID: "unit", Attempt: job.CapturedRun{Ref: "out2", ExtraArgs: args()}}},
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

// The rejection is the next holder's evidence, so a clone handed out of
// the store must not alias the slices one level down. Asserted across
// two clones for the reason TestJobCloneCopiesEverySliceField gives.
func TestJobCloneCopiesTheRejection(t *testing.T) {
	t.Parallel()

	refs := func() []string { return append(make([]string, 0, 4), "out1") }
	unmet := func() []job.VerificationStatus {
		return append(make([]job.VerificationStatus, 0, 4), job.VerificationStatus{ID: "unit", Violations: refs()})
	}
	orig := job.Job{ID: "a", Rejection: &job.Rejection{Unmet: unmet(), Violations: refs()}}

	first, second := orig.Clone(), orig.Clone()
	for i, c := range []job.Job{first, second} {
		tag := fmt.Sprint(i)
		c.Rejection.Unmet[0].Violations = append(c.Rejection.Unmet[0].Violations, tag)
		c.Rejection.Violations = append(c.Rejection.Violations, tag)
	}
	first.Rejection.At = 9

	assert.Equal(t, []string{"out1", "1"}, second.Rejection.Unmet[0].Violations)
	assert.Equal(t, []string{"out1", "1"}, second.Rejection.Violations)
	assert.Equal(t, job.Job{ID: "a", Rejection: &job.Rejection{Unmet: unmet(), Violations: refs()}}, orig)
}

// A cap on attempts is declared like the rest of the spec, so applying a record that omits
// it clears it, and a negative cap is refused at the door.
func TestDeclarationCarriesMaxAttempts(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateDeclaration(job.Declaration{ID: "worker", MaxAttempts: 3}))
	assert.ErrorContains(t, ValidateDeclaration(job.Declaration{ID: "worker", MaxAttempts: -1}), "max_attempts is -1")

	var row job.Job
	job.Declaration{ID: "worker", MaxAttempts: 3}.Apply(&row)
	assert.Equal(t, 3, row.MaxAttempts)
	job.Declaration{ID: "worker"}.ApplySpec(&row)
	assert.Zero(t, row.MaxAttempts)
}

// A weight outside (0,1] is refused, never clamped, and an explicit 0 is out of range rather
// than read as omitted.
func TestDeclarationRefusesAWeightOutOfRange(t *testing.T) {
	t.Parallel()

	for _, w := range []float64{0, -0.5, 1.5, math.NaN()} {
		assert.ErrorContains(t, ValidateDeclaration(job.Declaration{ID: "worker", Weight: &w}), "outside (0,1]", "weight %v", w)
	}
	for _, w := range []float64{0.1, 0.5, 1} {
		require.NoError(t, ValidateDeclaration(job.Declaration{ID: "worker", Weight: &w}), "weight %v", w)
	}

	var row job.Job
	w := 0.9
	job.Declaration{ID: "worker", Weight: &w}.Apply(&row)
	assert.InDelta(t, 0.9, row.Weight, 0)
	row.Paused = 42
	job.Declaration{ID: "worker"}.ApplySpec(&row)
	assert.Zero(t, row.Weight, "an omitted weight clears the row's, which reads as DefaultJobWeight")
	assert.Equal(t, int64(42), row.Paused, "apply keeps a pause")
}

// The generation and the rejection are the store's, so a declaration
// never writes them: Apply leaves them where they were.
func TestDeclarationApplyLeavesStoreComputedFieldsAlone(t *testing.T) {
	t.Parallel()

	row := job.Job{
		ID:         "worker",
		Generation: 2,
		Rejection:  &job.Rejection{At: 1, ObservedGeneration: 2},
	}
	want := row
	want.Agent = job.Instructed("again")
	job.Declaration{ID: "worker", Agent: job.Instructed("again")}.Apply(&row)
	assert.Equal(t, want, row)
}

func TestJobSpecChangedSinceReadsAZeroStampAsUnknown(t *testing.T) {
	t.Parallel()

	assert.False(t, job.Job{Generation: 2}.SpecChangedSince(2), "same generation")
	assert.True(t, job.Job{Generation: 2}.SpecChangedSince(1), "an earlier spec")
	assert.False(t, job.Job{Generation: 2}.SpecChangedSince(0), "an unstamped record")
	assert.False(t, job.Job{}.SpecChangedSince(1), "a row with no generation")
}

// slices.Clone preserves nil, which is what keeps a row that stored null from coming back
// as [] through the JSON door.
func TestJobCloneKeepsNilSlicesNil(t *testing.T) {
	t.Parallel()

	c := job.Job{ID: "a"}.Clone()
	assert.Nil(t, c.Unattributed)
	assert.Nil(t, c.Releases)
	assert.Nil(t, c.Goals)
	assert.Nil(t, c.GateAttempts)
}

func TestDeclarationRejectsCyclicGoals(t *testing.T) {
	t.Parallel()

	err := ValidateDeclaration((job.Declaration{
		ID: "gated",
		Goals: []job.Goal{
			{ID: "unit", Check: job.LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"publish"}},
			{ID: "publish", Check: job.LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"unit"}},
		},
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dependencies contain a cycle")
}

// A path the guard can never match against a workspace-relative one used to be accepted
// and bound nothing, silently.
func TestDeclarationRefusesAPathOutsideTheWorkspace(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		decl job.Declaration
		want string
	}{
		{job.Declaration{ID: "j", Paths: job.Paths{Write: []string{"~/.claude/plans/**"}}}, `paths.write[0] "~/.claude/plans/**" begins with ~`},
		{job.Declaration{ID: "j", Paths: job.Paths{Write: []string{"api/**", "/abs/**"}}}, `paths.write[1] "/abs/**" is absolute`},
		{job.Declaration{ID: "j", Paths: job.Paths{Deny: []string{"../wt/**"}}}, `paths.deny[0] "../wt/**" climbs out of the workspace`},
		{job.Declaration{ID: "j", Paths: job.Paths{Read: []string{"docs/../../x"}}}, `paths.read[0] "docs/../../x" climbs out`},
	} {
		err := ValidateDeclaration(tt.decl)
		require.Error(t, err, tt.want)
		assert.Contains(t, err.Error(), tt.want)
	}
	assert.NoError(t, ValidateDeclaration(job.Declaration{ID: "j", ReadOnly: true, Paths: job.Paths{Read: []string{"docs/../api/**"}}}), "a path that cleans to one inside the workspace stands")
}

func TestDeclarationValidatesSignatureGoals(t *testing.T) {
	t.Parallel()

	shape := func(id string, symbols ...string) job.Goal {
		return job.Goal{ID: id, Kind: job.GoalKindSymbol, Expect: job.ExpectSignature, Symbols: symbols}
	}
	for _, tc := range []struct {
		name  string
		goals []job.Goal
		want  string
	}{
		{"one symbol, signature left for the fork to record", []job.Goal{shape("a", "Api")}, ""},
		{"one symbol with its signature", []job.Goal{{ID: "a", Kind: job.GoalKindSymbol, Expect: job.ExpectSignature, Symbols: []string{"Api"}, Signature: "func Api()"}}, ""},
		{"two symbols", []job.Goal{shape("a", "Api", "Old")}, "expects the signature of 2 symbols"},
		{"no symbol", []job.Goal{shape("a", " ")}, "names nothing to examine"},
		{"a signature on another expectation", []job.Goal{{ID: "a", Kind: job.GoalKindSymbol, Expect: job.ExpectPresent, Symbols: []string{"Api"}, Signature: "func Api()"}},
			`expects "present" and carries a signature`},
		{"two signatures for one symbol", []job.Goal{shape("a", "Api"), shape("b", "Api")}, `goals "a" and "b" both expect a signature of "Api"`},
		{"signatures for two symbols", []job.Goal{shape("a", "Api"), shape("b", "Old")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateDeclaration(job.Declaration{ID: "shaped", Goals: tc.goals})
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	assert.Contains(t, job.GoalExpects(), job.ExpectSignature)
}

func TestJobUnmetNamesWhatThisBuildLacks(t *testing.T) {
	t.Parallel()

	assert.Nil(t, job.Job{}.Unmet())
	assert.Nil(t, job.Job{Requires: []string{"claim-declarations"}}.Unmet())
	assert.Equal(t, []string{"jobs-v99", "x"}, job.Job{Requires: []string{"jobs-v99", "signature-goals", "x"}}.Unmet())
}

func TestServerRunNeedsNoAgentAndTheServerAsRegistrar(t *testing.T) {
	t.Parallel()

	server := job.Origin{EntryPoint: job.EntryPointServer}
	assert.True(t, job.Job{RegisteredBy: server}.ServerRun())
	assert.False(t, job.Job{RegisteredBy: server, Agent: &job.Agent{}}.ServerRun(), "an agent section is a session's work")
	assert.False(t, job.Job{RegisteredBy: job.Origin{EntryPoint: job.EntryPointCLI}}.ServerRun())
}

func TestDeclarationRefusesABlankBreaksEntry(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateDeclaration(job.Declaration{ID: "worker", Breaks: []string{"--old-flag", "api.Open"}}))
	err := ValidateDeclaration(job.Declaration{ID: "worker", Breaks: []string{"--old-flag", "  "}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "breaks[1] is blank")
}

// The record is the whole spec, so applying one that omits breaks clears them: a job
// keeps the right to break something only while its declaration says so.
func TestDeclarationApplyCopiesBreaksAndClearsAnOmittedOne(t *testing.T) {
	t.Parallel()

	var row job.Job
	job.Declaration{ID: "worker", Breaks: []string{" --old-flag ", "", "api.Open"}}.Apply(&row)
	assert.Equal(t, []string{"--old-flag", "api.Open"}, row.Breaks)

	job.Declaration{ID: "worker"}.ApplySpec(&row)
	assert.Empty(t, row.Breaks)
}

func TestDeclarationIntegratesNamesMergeableBranchesOnce(t *testing.T) {
	t.Parallel()

	ok := job.Declaration{ID: "integrate", Integrates: []string{"done-a", "feature/done-b"}}
	require.NoError(t, ValidateDeclaration(ok))
	var row job.Job
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
		err := ValidateDeclaration(job.Declaration{ID: "integrate", Integrates: strings.Split(branches, ",")})
		require.Error(t, err, branches)
		assert.Contains(t, err.Error(), want, branches)
	}
	err := ValidateDeclaration(job.Declaration{ID: "integrate", Integrates: []string{"done-a"}, ReadOnly: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "an integration job merges branches, so it writes")
	assert.False(t, job.Job{ID: "worker"}.Integrating())
}

func TestDeclarationCarryingEnterDeclaresNothingElse(t *testing.T) {
	t.Parallel()

	schema := job.Schema{Version: job.APIVersion}
	require.NoError(t, ValidateDeclaration(job.Declaration{Schema: schema, ID: "worker", Enter: "a.go"}))
	err := ValidateDeclaration(job.Declaration{Schema: schema, ID: "worker", Enter: "a.go", Paths: job.Paths{Write: []string{"**"}}})
	assert.ErrorContains(t, err, "carries only api_version, id and enter")
}

func TestSameLeaseCheckComparesEveryField(t *testing.T) {
	t.Parallel()

	base := job.LeaseCheck{Target: "test", Script: "probe.buzz", Project: "p", Args: []string{"-run", "A"}, NoDefaultCharms: true}
	assert.True(t, job.SameLeaseCheck(&base, &base))
	same := base
	same.Args = []string{"-run", "A"}
	assert.True(t, job.SameLeaseCheck(&base, &same), "equal by value, not by identity")

	for i := range reflect.TypeFor[job.LeaseCheck]().NumField() {
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
			t.Fatalf("LeaseCheck.%s has kind %s, which this test does not know how to vary", reflect.TypeFor[job.LeaseCheck]().Field(i).Name, field.Kind())
		}
		assert.False(t, job.SameLeaseCheck(&base, &other), "a differing %s is a different check", reflect.TypeFor[job.LeaseCheck]().Field(i).Name)
	}
}

func TestSameLeaseCheckTreatsNilAsNoCheck(t *testing.T) {
	t.Parallel()

	declared := &job.LeaseCheck{Target: "test", Project: "."}
	assert.True(t, job.SameLeaseCheck(nil, nil))
	assert.False(t, job.SameLeaseCheck(nil, declared))
	assert.False(t, job.SameLeaseCheck(declared, nil))
}

// The parse is what makes a check comparable with a stored run, so the shapes it refuses
// are the point: a flag read as a positional binds evidence to a target nobody ran.
func TestParseLeaseCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		in    string
		want  job.LeaseCheck
		wants string
	}{
		{name: "target and project", in: "test internal/ledger", want: job.LeaseCheck{Target: "test", Project: "internal/ledger"}},
		{name: "the project defaults to the root", in: "ci", want: job.LeaseCheck{Target: "ci", Project: "."}},
		{
			name: "args ride past the separator",
			in:   "go::go-test . -- -run Ledger",
			want: job.LeaseCheck{Target: "go::go-test", Project: ".", Args: []string{"-run", "Ledger"}},
		},
		{
			name: "the charm flag is read, not refused",
			in:   "lint . --no-default-charms",
			want: job.LeaseCheck{Target: "lint", Project: ".", NoDefaultCharms: true},
		},
		{
			name: "the charm flag may lead",
			in:   "--no-default-charms generate .",
			want: job.LeaseCheck{Target: "generate", Project: ".", NoDefaultCharms: true},
		},
		{name: "another magus flag stays refused beside the charm flag", in: "lint . --no-default-charms -o json", wants: "carries the flag -o"},
		{name: "a flag before the separator", in: "-o json test .", wants: "carries the flag -o"},
		{name: "a third word", in: "test a b", wants: "carries 3 words"},
		{name: "nothing at all", in: "  ", wants: "names no target"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := job.ParseLeaseCheck(tt.in)
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

func TestScriptCheckNamesOneSubject(t *testing.T) {
	t.Parallel()

	gate := job.Goal{ID: "probe", Kind: job.GoalKindCheck, Check: job.LeaseCheck{Script: "probes/key.buzz"}}.Resolve()
	require.NoError(t, ValidateGoal(gate))

	both := gate
	both.Check.Target = "test"
	require.ErrorContains(t, ValidateGoal(both), "a check names one")

	scoped := gate
	scoped.Check.Project = "cmd/magus"
	require.ErrorContains(t, ValidateGoal(scoped), "belongs to the workspace")

	tested := gate
	tested.Check.Test = true
	require.NoError(t, ValidateGoal(tested))
}

func TestTestCheckIsAScriptChecksAlone(t *testing.T) {
	t.Parallel()

	target := job.Goal{ID: "unit", Kind: job.GoalKindCheck, Check: job.LeaseCheck{Target: "test", Project: ".", Test: true}}.Resolve()
	require.ErrorContains(t, ValidateGoal(target), `check target "test" sets test`)

	rec := job.Declaration{Schema: job.Schema{Version: job.APIVersion}, ID: "fix-parser", Check: &job.LeaseCheck{Target: "test", Test: true}}
	ps := DeclarationProblems(rec)
	require.Len(t, ps, 1)
	assert.Equal(t, "check.test", ps[0].Field)

	rec.Check = &job.LeaseCheck{Script: "probes/key.buzz", Test: true}
	require.Empty(t, DeclarationProblems(rec))
	var row job.Job
	rec.Apply(&row)
	require.NotNil(t, row.Check)
	assert.True(t, row.Check.Test, "the record's test reaches the row's check")
}

// An exited dependency is done editing, so nobody collecting it leaves its dependent
// queued forever: the clock starts at the exit, and only a declared or running one holds it.
func TestJobIdleSinceStartsAtAnExitedDependency(t *testing.T) {
	t.Parallel()

	row := job.Job{ID: "next", State: job.StateDeclared, Updated: 100, DependsOn: []string{"first"}}
	exited := job.Job{ID: "first", State: job.StateExited, Updated: 300}
	since, waiting := job.IdleSince([]job.Job{exited, row}, row)
	assert.False(t, waiting)
	assert.Equal(t, int64(300), since)
	assert.True(t, job.StaleAt([]job.Job{exited, row}, row, 300+3600, time.Hour))

	for _, state := range []job.State{job.StateRunning, job.StateDeclared} {
		dep := job.Job{ID: "first", State: state, Updated: 300}
		_, waiting = job.IdleSince([]job.Job{dep, row}, row)
		assert.True(t, waiting, "a %s dependency still holds the clock", state)
	}
}

func dependent(id string, state job.State, dependsOn string, paths ...string) job.Job {
	return job.Job{ID: id, State: state, Paths: job.Paths{Write: paths}, DependsOn: []string{dependsOn}}
}

func TestJobOverlapsOmitsADependentThatIsNotReady(t *testing.T) {
	t.Parallel()

	for _, state := range []job.State{job.StateDeclared, job.StateRunning, job.StateExited} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			rows := []job.Job{owner("dep", state, "internal/job"), dependent("waiter", job.StateDeclared, "dep", "internal/job")}
			report := job.NewList(rows)
			assert.Empty(t, report.Overlaps, "a job waiting on dep claims nothing dep could collide with")
			assert.Equal(t, []job.Block{{Job: "waiter", On: "dep", State: state}}, report.Blocked)
		})
	}

	t.Run("a third live job is not shielded by the waiter", func(t *testing.T) {
		t.Parallel()

		rows := []job.Job{
			owner("dep", job.StateRunning, "types"),
			dependent("waiter", job.StateDeclared, "dep", "internal/job"),
			owner("other", job.StateRunning, "internal/job"),
		}
		assert.Empty(t, job.NewList(rows).Overlaps)
	})
}

func TestJobOverlapsReportsADependentOnceEveryDependencyPasses(t *testing.T) {
	t.Parallel()

	rows := []job.Job{
		owner("dep", job.StateRunning, "types"),
		{ID: "waiter", State: job.StateDeclared, Paths: job.Paths{Write: []string{"internal/job"}}, DependsOn: []string{"dep", "dep2"}},
		owner("dep2", job.StatePass),
		owner("other", job.StateRunning, "internal/job"),
	}
	assert.Empty(t, job.NewList(rows).Overlaps, "one dependency still running keeps the waiter out")

	rows[0].State = job.StatePass
	report := job.NewList(rows)
	assert.Equal(t, []job.Overlap{{JobA: "waiter", JobB: "other", PathsA: []string{"internal/job"}, PathsB: []string{"internal/job"}}}, report.Overlaps)
	assert.Empty(t, report.Blocked)
}

func TestJobListNamesWhyAJobOwnsNothing(t *testing.T) {
	t.Parallel()

	for _, state := range []job.State{job.StateFail, job.StateNoReturn} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			rows := []job.Job{owner("dep", state), dependent("waiter", job.StateDeclared, "dep", "internal/job"), owner("other", job.StateRunning, "internal/job")}
			report := job.NewList(rows)
			assert.Empty(t, report.Overlaps)
			assert.Equal(t, []job.Block{{Job: "waiter", On: "dep", State: state}}, report.Blocked)
		})
	}

	t.Run("an undeclared dependency", func(t *testing.T) {
		t.Parallel()

		report := job.NewList([]job.Job{dependent("waiter", job.StateDeclared, "gone", "internal/job"), owner("other", job.StateRunning, "internal/job")})
		assert.Empty(t, report.Overlaps)
		assert.Equal(t, []job.Block{{Job: "waiter", On: "gone"}}, report.Blocked)
	})

	t.Run("a terminal dependent is not reported", func(t *testing.T) {
		t.Parallel()

		assert.Empty(t, job.NewList([]job.Job{owner("dep", job.StateFail), dependent("waiter", job.StateNoReturn, "dep")}).Blocked)
	})

	t.Run("json names the reason", func(t *testing.T) {
		t.Parallel()

		got, err := codec.Marshal(job.NewList([]job.Job{owner("dep", job.StateFail), dependent("waiter", job.StateDeclared, "dep")}))
		require.NoError(t, err)
		assert.Contains(t, string(got), `"blocked":[{"job":"waiter","on":"dep","state":"fail"}]`)
	})
}

func TestJobListFlagsAQueuedJobStaleOnlyOnceItsDependenciesEnd(t *testing.T) {
	t.Parallel()

	const now, hour = int64(100_000), int64(3600)
	rows := []job.Job{
		{ID: "dep", State: job.StateRunning, Registered: now, Updated: now},
		{ID: "queued", State: job.StateDeclared, DependsOn: []string{"dep"}, Updated: now - 3*hour},
		{ID: "lone", State: job.StateDeclared, Updated: now - 3*hour},
	}
	assert.Equal(t, []string{"lone"}, job.NewList(rows).Flag(now, 2*time.Hour).Stale, "a live dependency keeps its waiter queued")

	rows[0].State, rows[0].Updated = job.StatePass, now-hour
	assert.Equal(t, []string{"lone"}, job.NewList(rows).Flag(now, 2*time.Hour).Stale, "the waiter's clock starts at its dependency's end")
	assert.Equal(t, []string{"queued", "lone"}, job.NewList(rows).Flag(now+2*hour, 2*time.Hour).Stale)
}

// JobAncestors walks nearest first, stops at a parent the rows do not carry and at a
// cycle, and allocates only the chain it returns: every list asks it of every live row,
// and a per-call index of the whole store made one list quadratic in allocations.
func TestJobAncestors(t *testing.T) {
	rows := []job.Job{
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
		for _, a := range job.Ancestors(rows, id) {
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
		rows = append(rows, job.Job{ID: fmt.Sprintf("filler-%d", i)})
	}
	allocs := testing.AllocsPerRun(10, func() { job.Ancestors(rows, "leaf") })
	assert.LessOrEqual(t, allocs, 4.0, "only the returned chain allocates, whatever the store's size")
}

// A footprint that is not known still says so on the wire: footprint_known is never
// omitted, so a reader cannot take a missing footprint for an empty one.
func TestFootprintsCrossJSON(t *testing.T) {
	t.Parallel()

	status, err := codec.Marshal(job.Status{Job: "a", FootprintReason: "git does not report changed regions (RegionReporter)"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"job":"a","verified":false,"footprint_known":false,"footprint_reason":"git does not report changed regions (RegionReporter)","lines_added":0,"lines_deleted":0,"symbol_delta_known":false}`, string(status))

	status, err = codec.Marshal(job.Status{Job: "a", FootprintKnown: true, Footprint: []job.RegionChange{
		{File: job.FileChange{Path: "a.go"}, Side: job.RegionNew, Lines: [2]int{1, 4}, Declaration: "func X() {", Driver: "golang"},
	}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"job":"a","verified":false,"footprint_known":true,"footprint":[`+
		`{"file":{"path":"a.go"},"side":"new","lines":[1,4],"declaration":"func X() {","driver":"golang"}],"lines_added":0,"lines_deleted":0,"symbol_delta_known":false}`, string(status))

	status, err = codec.Marshal(job.Status{Job: "a", ObservedGeneration: 2, PriorGeneration: 1})
	require.NoError(t, err)
	assert.JSONEq(t, `{"job":"a","verified":false,"footprint_known":false,"lines_added":0,"lines_deleted":0,"symbol_delta_known":false,`+
		`"observed_generation":2,"prior_generation":1}`, string(status))

	overlap, err := codec.Marshal(job.Overlap{JobA: "a", JobB: "b", PathsA: []string{"x"}, PathsB: []string{"x"},
		Footprint: &job.OverlapFootprint{Verdict: job.FootprintShared, Shared: []string{"x/a.go#func X() {"}}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"job_a":"a","job_b":"b","paths_a":["x"],"paths_b":["x"],`+
		`"footprint":{"verdict":"shared","shared":["x/a.go#func X() {"]}}`, string(overlap))
}

func TestDeclarationProblemsCollectsEveryProblem(t *testing.T) {
	t.Parallel()
	d := job.Declaration{
		ID:          "bad id",
		Parent:      "bad parent",
		DependsOn:   []string{"ok", "bad dep"},
		State:       "nope",
		Timeout:     "soon",
		MaxAttempts: -1,
		Checkout:    "rel/dir",
		Paths:       job.Paths{Write: []string{"a.go", "../b.go"}},
		Breaks:      []string{" "},
		Check:       &job.LeaseCheck{Target: "test", Project: "."},
		Deviations: []job.Deviation{
			{Code: job.Code(JobRecordInvalid), Subject: "x", Reason: "r"},
			{Code: job.Code(GateNotRun)},
		},
		Goals: []job.Goal{
			{ID: "a", Kind: job.GoalKindPaths},
			{ID: "a", Kind: job.GoalKindPaths, Paths: []string{"x"}, DependsOn: []string{"missing"}},
		},
	}

	ps := DeclarationProblems(d)

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
		"MGS3037 paths.write[1]",
		"MGS3037 breaks[0]",
		"MGS3037 deviations[0].code",
		"MGS3037 deviations[1].subject",
		"MGS3037 deviations[1].reason",
		"MGS3038 goals[0].paths",
		"MGS3038 goals[1].id",
		"MGS3038 goals[1].depends_on[0]",
	}, fields)
	err := ValidateDeclaration(d)
	require.ErrorIs(t, err, JobRecordInvalid)
	require.ErrorIs(t, err, JobGoalInvalid)
}

func TestDeclarationProblemsOfAValidRecordIsEmpty(t *testing.T) {
	t.Parallel()
	d := job.Declaration{
		ID:         "wave/a",
		Paths:      job.Paths{Write: []string{"types/job.go"}},
		Check:      &job.LeaseCheck{Target: "go-test", Project: "."},
		Deviations: []job.Deviation{{Code: job.Code(GateNotRun), Subject: "lint .", Reason: "the root runs it"}},
		Goals:      []job.Goal{{ID: "names", Kind: job.GoalKindSymbol, Symbols: []string{"X"}, DependsOn: []string{job.PrimaryGoalID}}},
	}

	assert.Empty(t, DeclarationProblems(d))
	require.NoError(t, ValidateDeclaration(d))
}

func TestDeclarationProblemsReportsEachGoalCycleOnce(t *testing.T) {
	t.Parallel()
	d := job.Declaration{ID: "a", Goals: []job.Goal{
		{ID: "x", Kind: job.GoalKindPaths, Paths: []string{"p"}, DependsOn: []string{"y"}},
		{ID: "y", Kind: job.GoalKindPaths, Paths: []string{"p"}, DependsOn: []string{"x"}},
		{ID: "z", Kind: job.GoalKindPaths, Paths: []string{"p"}, DependsOn: []string{"z"}},
	}}

	ps := DeclarationProblems(d)

	require.Len(t, ps, 2)
	assert.Equal(t, "goals[0].depends_on", ps[1].Field)
	assert.Contains(t, ps[1].Message, `cycle at "x"`)
	assert.Equal(t, "goals[2].depends_on[0]", ps[0].Field)
	assert.Contains(t, ps[0].Message, "cannot depend on itself")
}

func TestDeclarationApplyCopiesDeviations(t *testing.T) {
	t.Parallel()
	deviations := []job.Deviation{{Code: job.Code(GateNotRun), Subject: "lint .", Reason: "the root runs it"}}
	var row job.Job

	job.Declaration{ID: "a", Deviations: deviations}.Apply(&row)
	deviations[0].Reason = "changed"

	assert.Equal(t, "the root runs it", row.Deviations[0].Reason)
}

func TestGoalProblemsNameTheFieldUnderTheGoal(t *testing.T) {
	t.Parallel()
	g := job.Goal{ID: "g", Kind: job.GoalKindSymbol, Expect: job.ExpectSignature, Symbols: []string{"A", "B"}, Paths: []string{"x"}}

	ps := FaultProblems(g.Faults())

	require.Len(t, ps, 2)
	assert.Equal(t, "paths", ps[0].Field)
	assert.Equal(t, "symbols", ps[1].Field)
	for _, p := range ps {
		assert.Equal(t, JobGoalInvalid, p.Code)
	}
	require.ErrorIs(t, ValidateGoal(g), JobGoalInvalid)
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
		assert.Equalf(t, tc.want, job.PathWithin(tc.inner, tc.outer), "PathWithin(%q, %q)", tc.inner, tc.outer)
	}
}

func TestParseClaimEvidence(t *testing.T) {
	t.Parallel()

	for because, want := range map[string]job.ClaimEvidence{
		"refs:hint.VCSAdd":         {Kind: job.ClaimRefs, Subject: "hint.VCSAdd"},
		"test:cmd/magus/vcs.go":    {Kind: job.ClaimTest, Subject: "cmd/magus/vcs.go"},
		"output:.:config-generate": {Kind: job.ClaimOutput, Subject: ".:config-generate"},
		"released":                 {Kind: job.ClaimReleased},
	} {
		got, err := job.ParseClaimEvidence(because)
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
		_, err := job.ParseClaimEvidence(because)
		require.Error(t, err, because)
		assert.Contains(t, err.Error(), refused)
	}
}

func TestAClaimRecordDeclaresNothingElse(t *testing.T) {
	t.Parallel()

	rec := job.Declaration{Schema: job.Schema{Version: job.APIVersion}, ID: "cli-ls-files", Claim: []job.ClaimRequest{{Path: "internal/guard/shell.go", Because: "refs:hint.VCSAdd"}}}
	assert.Empty(t, DeclarationProblems(rec))

	rec.Agent = job.Instructed("more")
	rec.Claim = append(rec.Claim, job.ClaimRequest{Path: "../x.go", Because: "released"}, job.ClaimRequest{Path: "internal/**", Because: "maybe"})
	var fields []string
	for _, p := range DeclarationProblems(rec) {
		fields = append(fields, p.Field)
	}
	assert.Equal(t, []string{"claim", "claim[1].path", "claim[2].path", "claim[2].because"}, fields)
}

func TestDeclarationCarriesMaxClaims(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateDeclaration(job.Declaration{ID: "worker", MaxClaims: 4}))
	assert.ErrorContains(t, ValidateDeclaration(job.Declaration{ID: "worker", MaxClaims: -1}), "max_claims is -1")

	var row job.Job
	job.Declaration{ID: "worker", MaxClaims: 4}.Apply(&row)
	assert.Equal(t, 4, row.MaxClaims)
	job.Declaration{ID: "worker"}.ApplySpec(&row)
	assert.Zero(t, row.MaxClaims)
}

// A pause holds until its bound and not a second past it; a pause naming no bound holds
// until it is lifted.
func TestJobPausedAtLapsesAtItsBound(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		row  job.Job
		want bool
	}{
		"not paused":       {row: job.Job{PausedUntil: 200}, want: false},
		"paused, no bound": {row: job.Job{Paused: 10, PauseReason: "busy"}, want: true},
		"inside its bound": {row: job.Job{Paused: 10, PauseReason: "busy", PausedUntil: 101}, want: true},
		"at its bound":     {row: job.Job{Paused: 10, PauseReason: "busy", PausedUntil: 100}, want: false},
	}
	for name, c := range cases {
		assert.Equal(t, c.want, c.row.PausedAt(100), name)
	}
}

func TestParseClaimEvidenceReadsADeadHolder(t *testing.T) {
	t.Parallel()

	e, err := job.ParseClaimEvidence("dead:api/migrate")
	require.NoError(t, err)
	assert.Equal(t, job.ClaimEvidence{Kind: job.ClaimDeadHolder, Subject: "api/migrate"}, e)
	assert.Equal(t, "dead:api/migrate", e.String())

	_, err = job.ParseClaimEvidence("dead")
	assert.EqualError(t, err, `"dead" names no dead subject`)
}

type deliverySuite struct {
	suite.Suite
}

func TestDelivery(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(deliverySuite))
}

func (s *deliverySuite) TestEachDeliveryIsAccepted() {
	for _, d := range job.Deliveries() {
		s.Require().NoError(ValidateDeclaration(job.Declaration{ID: "worker", Delivery: d}), d)
	}
}

func (s *deliverySuite) TestAnUnknownDeliveryIsRefusedNamingTheSet() {
	err := ValidateDeclaration(job.Declaration{ID: "worker", Delivery: "merge"})
	s.Require().ErrorContains(err, `delivery "merge" is not a delivery`)
	s.Require().ErrorContains(err, "fold, separate, stacked")
}

func (s *deliverySuite) TestAParentHeadOffAStackedJobIsRefused() {
	err := ValidateDeclaration(job.Declaration{ID: "worker", Delivery: job.DeliveryFold, ParentHead: "feat/a"})
	s.Require().ErrorContains(err, "this job is not stacked")
}

func (s *deliverySuite) TestApplyCarriesDeliveryAndTheParentHead() {
	var row job.Job
	job.Declaration{ID: "worker", Delivery: job.DeliveryStacked, ParentHead: " feat/a "}.Apply(&row)
	var want job.Job
	job.Declaration{ID: "worker"}.Apply(&want)
	want.Delivery, want.ParentHead = job.DeliveryStacked, "feat/a"
	s.Equal(want, row)

	job.Declaration{ID: "worker"}.ApplySpec(&row)
	s.Empty(row.Delivery, "a spec naming no delivery clears it")
	s.Empty(row.ParentHead)
}

func TestHoldsNoBuildSeat(t *testing.T) {
	check := job.Goal{Kind: job.GoalKindCheck, Check: job.LeaseCheck{Target: "test", Project: "."}}
	assert.True(t, job.Job{ReadOnly: true}.HoldsNoBuildSeat())
	assert.False(t, job.Job{ReadOnly: true, Goals: []job.Goal{check}}.HoldsNoBuildSeat(), "a check runs a target")
	assert.False(t, job.Job{ReadOnly: true, Paths: job.Paths{Write: []string{"a/**"}}}.HoldsNoBuildSeat())
	assert.False(t, job.Job{}.HoldsNoBuildSeat(), "a writer is not read-only")
}
