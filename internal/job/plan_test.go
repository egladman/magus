package job

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeclareStampsTheDeadlineFromTheTimeout(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	within := func(t *testing.T, got, want int64) {
		t.Helper()
		assert.InDelta(t, want, got, 5, "deadline %d, want about %d", got, want)
	}

	var timed types.Job
	Declare(types.Declaration{ID: "a", Timeout: "30m"}, time.Hour)(&timed)
	within(t, timed.Deadline, now+1800)

	var defaulted types.Job
	Declare(types.Declaration{ID: "a"}, time.Hour)(&defaulted)
	within(t, defaulted.Deadline, now+3600)

	unbound := types.Job{Deadline: now + 60}
	Declare(types.Declaration{ID: "a"}, 0)(&unbound)
	assert.Zero(t, unbound.Deadline, "a re-declaration with no timeout and no default carries no bound")
}

func TestMergeTimeoutStampsAndClearsTheDeadline(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()
	got := applyMerge(t, map[string]any{"timeout": "2h"}, types.Job{ID: "u1"})
	assert.InDelta(t, now+7200, got.Deadline, 5)

	kept := applyMerge(t, map[string]any{"state": "running"}, types.Job{ID: "u1", Deadline: 42})
	assert.Equal(t, int64(42), kept.Deadline, "a put naming no timeout keeps the bound")

	cleared := applyMerge(t, map[string]any{"timeout": ""}, types.Job{ID: "u1", Deadline: 42})
	assert.Zero(t, cleared.Deadline)

	_, err := DecodePut(map[string]any{"timeout": "soon"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")

	_, err = DecodePut(map[string]any{"deadline": 5})
	require.Error(t, err, "the instant is the store's to stamp")
}

func TestJobListFlagsOverdueOrphansAndStaleJobs(t *testing.T) {
	t.Parallel()

	const now = 10_000
	rows := []types.Job{
		{ID: "root", State: types.StatePass, Updated: now},
		{ID: "root/orphan", Parent: "root", State: types.StateRunning, Updated: now},
		{ID: "live", State: types.StateRunning, Updated: now},
		{ID: "live/overdue", Parent: "live", State: types.StateRunning, Deadline: now - 1, Updated: now},
		{ID: "live/stale", Parent: "live", State: types.StateDeclared, Updated: now - 3600},
		{ID: "done", State: types.StateFail, Deadline: 1, Updated: 1},
	}

	got := types.NewJobList(rows).Flag(now, 30*time.Minute)
	want := types.NewJobList(rows)
	want.Overdue, want.Orphans, want.Stale = []string{"live/overdue"}, []string{"root/orphan"}, []string{"live/stale"}
	assert.Equal(t, want, got)

	unset := types.NewJobList(rows).Flag(now, 0)
	assert.Empty(t, unset.Stale, "an unset stale_after flags nothing")
}

func TestForkLimitProblemsNameTheKey(t *testing.T) {
	t.Parallel()

	rows := []types.Job{
		{ID: "root", State: types.StateRunning},
		{ID: "root/a", Parent: "root", State: types.StateRunning},
		{ID: "root/b", Parent: "root", State: types.StatePass},
		{ID: "other", State: types.StateRunning},
	}

	err := forkLimitProblems(rows, "root/a/deep", "root/a", config.Jobs{MaxDepth: 1}).Err()
	require.ErrorIs(t, err, types.JobForkLimitExceeded)
	assert.Contains(t, err.Error(), "jobs.max_depth")
	assert.Contains(t, err.Error(), "root/a/deep")
	assert.Empty(t, forkLimitProblems(rows, "root/c", "root", config.Jobs{MaxDepth: 1}))

	err = forkLimitProblems(rows, "root/c", "root", config.Jobs{MaxLive: 2}).Err()
	require.ErrorIs(t, err, types.JobForkLimitExceeded)
	assert.Contains(t, err.Error(), "jobs.max_live")
	assert.Empty(t, forkLimitProblems(rows, "root/c", "root", config.Jobs{MaxLive: 3}),
		"a finished job and another root's jobs do not count")
	assert.Empty(t, forkLimitProblems(rows, "root/a", "root", config.Jobs{MaxLive: 2}),
		"re-declaring a live job is not a new one")
	assert.Len(t, forkLimitProblems(rows, "root/a/deep", "root/a", config.Jobs{MaxDepth: 1, MaxLive: 2}), 2, "both limits at once")

	assert.Empty(t, forkLimitProblems(rows, "root/a/deep/deeper", "root/a/deep", config.Jobs{}), "unset is unlimited")
}

func TestForkLimitRefusesAClaimCapPastJobsMaxClaims(t *testing.T) {
	t.Parallel()

	limits := config.Jobs{MaxClaims: 4}
	assert.Empty(t, claimCeilingProblems(types.Job{ID: "w", MaxClaims: 4}, limits))
	err := claimCeilingProblems(types.Job{ID: "w", MaxClaims: 5}, limits).Err()
	require.ErrorIs(t, err, types.JobForkLimitExceeded)
	assert.Contains(t, err.Error(), "5 claims (max_claims on job w)")
	assert.Contains(t, err.Error(), "ceiling of 4 (jobs.max_claims in magus.yaml)")
}

func TestAmbiguousSymbolProblemsNameEveryDefinition(t *testing.T) {
	t.Parallel()

	read := func(_ context.Context, name, _ string) (SymbolFact, bool) {
		switch name {
		case "Validate":
			return SymbolFact{DefinedIn: []string{"types/job.go"}, SameNameDefinitions: []string{"internal/config/validate.go", "types/job.go"}}, true
		case "Cold":
			return SymbolFact{}, false
		}
		return SymbolFact{DefinedIn: []string{"internal/job/plan.go"}}, true
	}
	gate := func(names ...string) []types.Goal {
		return []types.Goal{
			{ID: "check", Check: types.LeaseCheck{Target: "test", Project: "."}},
			{ID: "sym", Kind: types.GoalKindSymbol, Symbols: names},
		}
	}

	ps := ambiguousSymbolProblems(t.Context(), gate("Declare", "Validate"), read)
	require.Len(t, ps, 1)
	p := ps[0]
	assert.Contains(t, p.Message, `"Validate"`)
	assert.Contains(t, p.Message, "internal/config/validate.go")
	assert.Contains(t, p.Message, "types/job.go")
	assert.Equal(t, types.JobProblem{Code: types.JobGoalInvalid, Field: "goals[1].symbols[1]", Message: p.Message,
		Fix: "name one by the symbol id `magus refs Validate` prints"}, p)

	assert.Empty(t, ambiguousSymbolProblems(t.Context(), gate("Declare", "Cold"), read), "a name the graph cannot read fails open at declaration")
	assert.Empty(t, ambiguousSymbolProblems(t.Context(), gate("Declare"), nil))
}

type cannedSymbolGraph struct {
	nodes []types.KnowledgeMatch
	defs  map[string]string
	// signatures are the indexed signatures by node id, matched as text.
	signatures map[string]string
}

func (g cannedSymbolGraph) SignatureMatches(id, declared string) (string, bool, bool) {
	indexed, ok := g.signatures[id]
	return indexed, ok && indexed == declared, ok
}

func (g cannedSymbolGraph) Refs(ref string) (types.KnowledgeRefsOutput, bool) {
	for _, n := range g.nodes {
		if n.ID == ref || n.Label == ref {
			return types.KnowledgeRefsOutput{Symbol: n.ID, Label: n.Label, Defs: []types.KnowledgeRefSite{{File: g.defs[n.ID]}}}, true
		}
	}
	return types.KnowledgeRefsOutput{}, false
}

func (g cannedSymbolGraph) Resolve(input string, _ int) []types.KnowledgeMatch {
	var out []types.KnowledgeMatch
	for _, n := range g.nodes {
		if strings.Contains(n.Label, input) {
			out = append(out, n)
		}
	}
	return out
}

func TestGraphSymbolsListsEveryDefinitionOfABareName(t *testing.T) {
	t.Parallel()

	g := cannedSymbolGraph{
		nodes: []types.KnowledgeMatch{
			{ID: "symbol:config/Validate().", Kind: types.KindSymbol, Label: "Validate"},
			{ID: "symbol:types/Declaration#Validate().", Kind: types.KindSymbol, Label: "Validate"},
			{ID: "symbol:types/ValidateAll().", Kind: types.KindSymbol, Label: "ValidateAll"},
		},
		defs: map[string]string{
			"symbol:config/Validate().":            "internal/config/validate.go",
			"symbol:types/Declaration#Validate().": "types/job.go",
			"symbol:types/ValidateAll().":          "types/all.go",
		},
	}
	fact, ok := GraphSymbols(g)(t.Context(), "Validate", "")
	require.True(t, ok)
	assert.Equal(t, []string{"internal/config/validate.go", "types/job.go"}, fact.SameNameDefinitions)

	exact, ok := GraphSymbols(g)(t.Context(), "symbol:types/Declaration#Validate().", "")
	require.True(t, ok)
	assert.Empty(t, exact.SameNameDefinitions, "a full symbol id names one definition")
}

func TestGraphSymbolsResolvesAPackageQualifiedName(t *testing.T) {
	t.Parallel()

	g := cannedSymbolGraph{
		nodes: []types.KnowledgeMatch{
			{ID: "symbol:internal/job Declare().", Kind: types.KindSymbol, Label: "Declare"},
			{ID: "symbol:types/Declare().", Kind: types.KindSymbol, Label: "Declare"},
			{ID: "symbol:types/Declaration#Validate().", Kind: types.KindSymbol, Label: "Validate"},
		},
		defs: map[string]string{
			"symbol:internal/job Declare().":       "internal/job/plan.go",
			"symbol:types/Declare().":              "types/declare.go",
			"symbol:types/Declaration#Validate().": "types/job.go",
		},
	}
	read := GraphSymbols(g)
	for name, want := range map[string][]string{
		"job.Declare":                {"internal/job/plan.go"},
		"types.Declare":              {"types/declare.go"},
		"types.Declaration#Validate": {"types/job.go"},
		"job.Missing":                nil,
		"types.Validate":             nil,
	} {
		fact, ok := read(t.Context(), name, "")
		require.True(t, ok, name)
		assert.Equal(t, want, fact.DefinedIn, name)
	}

	bare, ok := read(t.Context(), "Declare", "")
	require.True(t, ok)
	assert.Equal(t, []string{"internal/job/plan.go", "types/declare.go"}, bare.SameNameDefinitions, "a bare name is still ambiguous")
}

// A gate graded on a scoped run says so beside its mark, so an integration grade shows what
// the run covered.
func TestRenderGatesShowsTheScopeOfARun(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	RenderGates(&out, types.JobStatus{Job: "unit", Gates: []types.GateStatus{
		{ID: "check", Verified: true, OutputRef: "out1", Scope: "-- ./internal/job -run Wait"},
		{ID: "lint", Verified: true, OutputRef: "out2"},
	}})
	assert.Equal(t, "unit: 2 of 2 goal(s) met\n  [met] check, a run scoped with `-- ./internal/job -run Wait`\n  [met] lint\n", out.String())
}

func TestRenderGatesSaysTheGraphWasStale(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	RenderGates(&out, types.JobStatus{
		Job:          "unit",
		Gates:        []types.GateStatus{{ID: "sym", Verified: true}},
		StaleIndexes: []string{".", "console"},
	})
	assert.Contains(t, out.String(), "1 of 1 goal(s) met")
	assert.Contains(t, out.String(), "stale")
	assert.Contains(t, out.String(), "., console")
	assert.Contains(t, out.String(), "graph build")
	assert.Contains(t, out.String(), "symbol goals were not graded", "describe grades nothing over a stale index")
	assert.NotContains(t, out.String(), "graded against an index older")
}

func TestRenderGatesLabelsEvidenceOfAnEarlierSpec(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	RenderGates(&out, types.JobStatus{
		Job:                "unit",
		Gates:              []types.GateStatus{{ID: "check"}},
		ObservedGeneration: 3,
		PriorGeneration:    2,
	})
	assert.Equal(t, "unit: 0 of 1 goal(s) met\n  [unmet] check\n"+
		"prior evidence: taken at generation 2, the spec is now generation 3; run it again\n", out.String())

	out.Reset()
	RenderGates(&out, types.JobStatus{Job: "unit", Gates: []types.GateStatus{{ID: "check", Verified: true}}, ObservedGeneration: 3})
	assert.NotContains(t, out.String(), "prior evidence")
}

func TestRenderGatesReportsTheDelta(t *testing.T) {
	t.Parallel()

	render := func(status types.JobStatus) string {
		var out strings.Builder
		RenderGates(&out, status)
		return out.String()
	}
	gates := []types.GateStatus{{ID: "check", Verified: true}}

	measured := types.JobStatus{Job: "unit", Gates: gates, FootprintKnown: true, LinesAdded: 4, LinesDeleted: 9,
		SymbolDeltaKnown: true, UndeclaredExports: []string{"Alpha", "Goal#Extra"}}
	assert.Equal(t, "unit: 1 of 1 goal(s) met\n  [met] check\n"+
		"net lines: +4 -9 (-5)\n"+
		"exports no goal declares: 2\n  Alpha\n  Goal#Extra\n"+
		"left defined with no use: none\n", render(measured))
	assert.Equal(t, render(measured), render(measured), "two renders of one status are one text")

	unread := types.JobStatus{Job: "unit", Gates: gates, FootprintReason: "the job was declared without a checkpoint"}
	assert.Equal(t, "unit: 1 of 1 goal(s) met\n  [met] check\n"+
		"net lines: not measured, the job was declared without a checkpoint\n"+
		"exports no goal declares: not measured, the symbol graph or the diff could not be read\n"+
		"left defined with no use: not measured, the symbol graph or the diff could not be read\n", render(unread))

	assert.Equal(t, "unit: 1 of 1 goal(s) met\n  [met] check\n", render(types.JobStatus{Job: "unit", Gates: gates}),
		"a grade no diff was read for, such as an integration, measures nothing")
}

// symbolReader answers from facts by name, and false for a name it holds none for.
func symbolReader(facts map[string]SymbolFact) SymbolReader {
	return func(_ context.Context, name, _ string) (SymbolFact, bool) {
		fact, ok := facts[name]
		return fact, ok
	}
}

func TestResolveSymbolGoalsFreezesSignatures(t *testing.T) {
	t.Parallel()

	read := symbolReader(map[string]SymbolFact{
		"Api":   {DefinedIn: []string{"api.go"}, Signature: "func Api(ctx context.Context) error"},
		"Stub":  {},
		"Bare":  {DefinedIn: []string{"bare.go"}},
		"Twice": {DefinedIn: []string{"a.go"}, Signature: "func Twice()", SameNameDefinitions: []string{"a.go", "b.go"}},
	})
	goal := func(name, signature string) types.Goal {
		return types.Goal{ID: "shape", Kind: types.GoalKindSymbol, Expect: types.ExpectSignature, Symbols: []string{name}, Signature: signature}
	}
	row := func(g types.Goal) types.Job {
		return types.Job{ID: "j", ReadOnly: true, Goals: []types.Goal{g}}
	}

	frozen, claims, err := ResolveSymbolGoals(t.Context(), row(goal("Api", "")), read)
	require.NoError(t, err)
	assert.Empty(t, claims)
	assert.Equal(t, "func Api(ctx context.Context) error", frozen.Goals[0].Signature, "an empty signature records the indexed one")

	declared, _, err := ResolveSymbolGoals(t.Context(), row(goal("Api", "func Api() error")), read)
	require.NoError(t, err)
	assert.Equal(t, "func Api() error", declared.Goals[0].Signature, "a declared signature is the contract as written")
	_, _, err = ResolveSymbolGoals(t.Context(), row(goal("New", "func New()")), nil)
	require.NoError(t, err, "a declared signature needs no graph, even for a symbol not written yet")

	for _, tc := range []struct {
		name, symbol string
		read         SymbolReader
		want         string
	}{
		{"no reader", "Api", nil, "this door reads no symbol graph"},
		{"an unreadable graph", "Cold", read, "the symbol graph could not be read"},
		{"a symbol the index does not define", "Stub", read, "write the stub, run `" + hint.GraphBuild.String() + "`, and fork again"},
		{"a symbol with no indexed signature", "Bare", read, "holds none for it to record"},
		{"an ambiguous name", "Twice", read, "resolves to 2 definitions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := ResolveSymbolGoals(t.Context(), row(goal(tc.symbol, "")), tc.read)
			require.Error(t, err, "a signature that cannot be frozen is refused, never skipped")
			assert.Contains(t, err.Error(), `goal "shape"`)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestResolveSymbolGoalsDerivesClaims(t *testing.T) {
	t.Parallel()

	read := symbolReader(map[string]SymbolFact{
		"Api":       {DefinedIn: []string{"api.go"}, Signature: "func Api()"},
		"Resolve":   {DefinedIn: []string{"types/job.go"}, Signature: "func (Goal).Resolve() Goal"},
		"Update":    {DefinedIn: []string{"store.go"}, Signature: "func (s *Store) Update(id string) error"},
		"Goal":      {DefinedIn: []string{"types/job.go"}, Signature: "type Goal struct"},
		"Field":     {DefinedIn: []string{"types/job.go"}, Signature: "struct field Signature string"},
		"Split":     {DefinedIn: []string{"a.go", "b.go"}, Signature: "func Split()"},
		"Covered":   {DefinedIn: []string{"internal/job/plan.go"}, Signature: "func Covered()"},
		"Unindexed": {DefinedIn: []string{"x.go"}},
	})
	symbol := func(id string, expect types.GoalExpect, names ...string) types.Goal {
		return types.Goal{ID: id, Kind: types.GoalKindSymbol, Expect: expect, Symbols: names}
	}
	row := types.Job{ID: "j", WritePaths: []string{"internal/job", "api.go#func Other"}, Goals: []types.Goal{
		symbol("moved", types.ExpectChanged, "Api", "Resolve", "Split", "Covered", "Unindexed", "Field"),
		symbol("gone", types.ExpectAbsent, "Update"),
		{ID: "shape", Kind: types.GoalKindSymbol, Expect: types.ExpectSignature, Symbols: []string{"Goal"}, Signature: "type Goal struct"},
		symbol("kept", types.ExpectPresent, "Api"),
		symbol("unused", types.ExpectUnreferenced, "Update"),
		{ID: "check", Check: types.LeaseCheck{Target: "test", Project: "."}},
	}}

	resolved, claims, err := ResolveSymbolGoals(t.Context(), row, read)
	require.NoError(t, err)
	want := []string{"api.go#func Api", "store.go#Store) Update", "types/job.go#Goal) Resolve", "types/job.go#type Goal"}
	assert.Equal(t, want, claims)
	assert.Equal(t, append([]string{"internal/job", "api.go#func Other"}, want...), resolved.WritePaths)
	assert.Equal(t, []string{"internal/job", "api.go#func Other"}, row.WritePaths, "the row handed in is not changed")

	readOnly := row
	readOnly.ReadOnly = true
	_, claims, err = ResolveSymbolGoals(t.Context(), readOnly, read)
	require.NoError(t, err)
	assert.Empty(t, claims, "a read-only row claims nothing")

	_, claims, err = ResolveSymbolGoals(t.Context(), row, nil)
	require.NoError(t, err, "only a signature goal needs a graph")
	assert.Empty(t, claims)
}

// A door that forks or applies holds a derived claim to the placement a declared one
// meets, and drops the one no diff driver places. A dry run computes the claims and writes
// nothing.
func TestApplyAndForkMergePlaceDerivedClaims(t *testing.T) {
	t.Parallel()

	root := testkit.NewRepo(t, map[string]string{".gitattributes": "*.go diff=golang\n", "run.go": claimGo, "notes.txt": "func Notes()\n"}).Root()
	read := symbolReader(map[string]SymbolFact{
		"A":     {DefinedIn: []string{"run.go"}, Signature: "func A(ctx int)"},
		"Notes": {DefinedIn: []string{"notes.txt"}, Signature: "func Notes()"},
	})
	goals := []types.Goal{{ID: "moved", Kind: types.GoalKindSymbol, Symbols: []string{"A", "Notes"}}}
	record := types.Declaration{ID: "wave/job", WritePaths: []string{"other.go"}, Check: forkCheck(), Goals: goals}

	s := NewStore(tempLocation(t, root))
	plan, err := Apply(t.Context(), s, []types.Declaration{record}, config.Jobs{}, read, nil, true)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	assert.Equal(t, []string{"other.go", "run.go#func A"}, plan[0].Next.WritePaths)
	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, rows, "a dry run writes nothing")

	stored, err := ForkMerge(t.Context(), s, "wave/job", func(u *types.Job) {
		u.Check, u.State, u.WritePaths, u.Goals = forkCheck(), types.StateDeclared, []string{"other.go"}, goals
	}, config.Jobs{}, read)
	require.NoError(t, err)
	assert.Equal(t, []string{"other.go", "run.go#func A"}, stored.WritePaths)
	assert.Equal(t, []string{"claim-declarations"}, stored.Requires, "an older magus reads a claim wrongly, so it may not write this row")

	unclaimed, err := ForkMerge(t.Context(), s, "wave/job", func(u *types.Job) {
		u.WritePaths, u.Goals = []string{"other.go"}, nil
	}, config.Jobs{}, read)
	require.NoError(t, err)
	assert.Nil(t, unclaimed.Requires, "a row that drops its last claim is writable by an older magus again")
}

// A record applied again with its signature goal still undeclared keeps the signature the
// first apply froze, so a holder's edit to the symbol never becomes the contract.
func TestApplyKeepsAFrozenSignature(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	record := types.Declaration{ID: "wave/job", WritePaths: []string{"api.go"}, Check: forkCheck(), Goals: []types.Goal{
		{ID: "shape", Kind: types.GoalKindSymbol, Expect: types.ExpectSignature, Symbols: []string{"Api"}},
	}}
	before := symbolReader(map[string]SymbolFact{"Api": {DefinedIn: []string{"api.go"}, Signature: "func Api()"}})
	after := symbolReader(map[string]SymbolFact{"Api": {DefinedIn: []string{"api.go"}, Signature: "func Api(n int)"}})

	_, err := Apply(t.Context(), s, []types.Declaration{record}, config.Jobs{}, before, nil, false)
	require.NoError(t, err)
	plan, err := Apply(t.Context(), s, []types.Declaration{record}, config.Jobs{}, after, nil, false)
	require.NoError(t, err)
	assert.Equal(t, "func Api()", plan[0].Next.Goals[0].Signature)
	assert.Equal(t, []string{"signature-goals"}, plan[0].Next.Requires)
}

func TestWaitRefusesPassWhileADescendantIsLive(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	child := types.Job{ID: row.ID + "/child", Parent: row.ID, State: types.StateRunning}
	grandchild := types.Job{ID: child.ID + "/leaf", Parent: child.ID, State: types.StateDeclared}
	finished := types.Job{ID: row.ID + "/done", Parent: row.ID, State: types.StatePass}
	rep := passingResult()
	seen := Observed{Changed: rep.ChangedPaths, ChangedKnown: true}

	status := VerifyGates(row, rep, passingRun, nil, []types.Job{row, child, grandchild, finished}, seen)
	assert.False(t, status.Verified)
	joined := strings.Join(status.Violations, "\n")
	assert.Contains(t, joined, child.ID)
	assert.Contains(t, joined, grandchild.ID)
	assert.NotContains(t, joined, finished.ID)
}

func TestGradeGatesInheritsAncestorSymbolGates(t *testing.T) {
	t.Parallel()

	parent := types.Job{
		ID: "rename", State: types.StateRunning, WritePaths: []string{"."},
		Goals: []types.Goal{
			{ID: "gone", Kind: types.GoalKindSymbol, Expect: types.ExpectAbsent, Symbols: []string{"OldName"}},
			{ID: "tests", Check: types.LeaseCheck{Target: "test", Project: "."}},
		},
	}
	child := types.Job{
		ID: "rename/api", Parent: "rename", State: types.StateRunning, WritePaths: []string{"api"},
		Goals: []types.Goal{{ID: "moved", Kind: types.GoalKindPaths, Expect: types.ExpectPresent, Paths: []string{"api"}}},
	}
	loc := declared(t, parent, child)
	read := func(_ context.Context, name, _ string) (SymbolFact, bool) {
		if name == "OldName" {
			return SymbolFact{DefinedIn: []string{"api/old.go"}}, true
		}
		return SymbolFact{}, true
	}

	status, err := GradeGates(t.Context(), NewStore(loc), child.ID, CheckpointObserver("", read))
	require.NoError(t, err)
	ids := make([]string, 0, len(status.Gates))
	for _, g := range status.Gates {
		ids = append(ids, g.ID)
	}
	assert.Contains(t, ids, "rename/gone", "a child is graded against its ancestors' symbol goals")
	assert.NotContains(t, ids, "rename/tests", "only symbol goals are inherited")
	assert.Contains(t, strings.Join(status.Violations, "\n"), `"OldName" is still defined in api/old.go`)
}

func TestVerifyHoldsTheClaimToTheObservedDiff(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	rep := passingResult()

	t.Run("a claimed path the diff does not show", func(t *testing.T) {
		t.Parallel()
		seen := Observed{Changed: []string{"internal/ledger/report.go"}, ChangedKnown: true, ChangedFrom: "abc"}
		status := VerifyGates(row, rep, passingRun, nil, nil, seen)
		assert.False(t, status.Verified)
		assert.Contains(t, strings.Join(status.Violations, "\n"), `"cmd/magus/ledger.go"`)
	})
	t.Run("no observed change inside the write paths", func(t *testing.T) {
		t.Parallel()
		claim := rep
		claim.ChangedPaths = []string{"internal/ledger/report.go"}
		seen := Observed{Changed: []string{"internal/ledger/report.go", "README.md"}, ChangedKnown: true}
		assert.True(t, VerifyGates(row, claim, passingRun, nil, nil, seen).Verified)

		outside := Observed{Changed: []string{"README.md"}, ChangedKnown: true}
		claim.ChangedPaths = nil
		status := VerifyGates(row, claim, passingRun, nil, nil, outside)
		assert.False(t, status.Verified)
		assert.Contains(t, strings.Join(status.Violations, "\n"), "inside its write paths")
	})
	t.Run("a diff nobody could read", func(t *testing.T) {
		t.Parallel()
		status := VerifyGates(row, rep, passingRun, nil, nil, Observed{})
		assert.False(t, status.Verified)
		assert.Contains(t, strings.Join(status.Violations, "\n"), "could not read")
	})
	t.Run("a read-only job needs no diff", func(t *testing.T) {
		t.Parallel()
		ro := row
		ro.ReadOnly, ro.WritePaths = true, nil
		claim := rep
		claim.ChangedPaths = nil
		assert.True(t, VerifyGates(ro, claim, passingRun, nil, nil, Observed{}).Verified)
	})
}

// TestForkMergeHoldsANewRowToTheLimits pins that magus\job.put forks under the same
// scope, limits and default timeout `magus job fork` applies, and so does a merge that
// brings an ended row back, while a merge onto a live row stays an update nothing refuses.
func TestForkMergeHoldsANewRowToTheLimits(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := NewStore(tempLocation(t, t.TempDir()))
	limits := config.Jobs{MaxDepth: 1, DefaultTimeout: time.Hour}
	merge := func(parent string) func(*types.Job) {
		return func(u *types.Job) { u.Parent, u.State, u.ReadOnly = parent, types.StateDeclared, true }
	}

	_, err := ForkMerge(ctx, s, "unscoped", func(u *types.Job) { u.State = types.StateDeclared }, limits, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "names no write paths and is not read-only")

	root, err := ForkMerge(ctx, s, "root", merge(""), limits, nil)
	require.NoError(t, err)
	assert.InDelta(t, time.Now().Add(time.Hour).Unix(), root.Deadline, 5, "a new row takes default_timeout")

	_, err = ForkMerge(ctx, s, "root/a", merge("root"), limits, nil)
	require.NoError(t, err)
	_, err = ForkMerge(ctx, s, "root/a/deep", merge("root/a"), limits, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jobs.max_depth")

	updated, err := ForkMerge(ctx, s, "root/a", func(u *types.Job) { u.Model = "opus" }, config.Jobs{MaxLive: 1}, nil)
	require.NoError(t, err, "an update is not a fork")
	assert.Equal(t, "opus", updated.Model)

	_, err = ForkMerge(ctx, s, "root/a", func(u *types.Job) { u.State = types.StateFail }, limits, nil)
	require.NoError(t, err)
	_, err = ForkMerge(ctx, s, "root/a", merge("root"), config.Jobs{MaxLive: 1}, nil)
	require.Error(t, err, "a fork onto an ended row is a new fork")
	assert.Contains(t, err.Error(), "jobs.max_live")
}

// A merge that moves a live row's write paths recomputes the proof it was forked with:
// the one it held graded paths it no longer claims.
func TestForkMergeRecomputesTheWriteProofOfMovedPaths(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := NewStore(tempLocation(t, loadableRoot(t)))
	writes := func(paths ...string) func(*types.Job) {
		return func(u *types.Job) {
			u.Check, u.State, u.WritePaths = forkCheck(), types.StateDeclared, paths
		}
	}

	_, err := ForkMerge(ctx, s, "wave/peer", writes("internal/guard/*.go"), config.Jobs{}, nil)
	require.NoError(t, err)
	row, err := ForkMerge(ctx, s, "wave/mine", writes("docs/x.md"), config.Jobs{}, nil)
	require.NoError(t, err)
	require.Equal(t, types.WriteProofDisjoint, row.WriteProof)

	row, err = ForkMerge(ctx, s, "wave/mine", func(u *types.Job) { u.Model = "opus" }, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Equal(t, types.WriteProofDisjoint, row.WriteProof, "paths that did not move keep their proof")

	row, err = ForkMerge(ctx, s, "wave/mine", writes("docs/x.md", "internal/guard/guard.go"), config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Equal(t, types.WriteProofOverlapping, row.WriteProof)
}

// MGS3018 does not depend on which call wrote the path: a put that adds a directory to a
// row that exists is refused as a fork naming it would be, and writes nothing.
func TestForkMergeRefusesADirectoryAddedToARowThatExists(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := NewStore(tempLocation(t, loadableRoot(t)))
	_, err := ForkMerge(ctx, s, "wave/job", func(u *types.Job) {
		u.Check, u.State, u.WritePaths = forkCheck(), types.StateDeclared, []string{"internal/job/store.go"}
	}, config.Jobs{}, nil)
	require.NoError(t, err)

	_, err = ForkMerge(ctx, s, "wave/job", func(u *types.Job) {
		u.WritePaths = append(u.WritePaths, "internal/**")
	}, config.Jobs{}, nil)
	require.ErrorIs(t, err, types.WritePathIsDirectory)
	rows, err := s.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"internal/job/store.go"}, rows[0].WritePaths)
}

// MGS3031 holds for a deny path a put adds to a row that exists, as it does at fork: a
// declaration deny on a file no diff driver reads is refused and writes nothing.
func TestForkMergeRefusesAnUngradableDenyPathAddedToARowThatExists(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := NewStore(tempLocation(t, testkit.NewRepo(t, map[string]string{".gitattributes": "*.go diff=golang\n", "run.go": "package run\n"}).Root()))
	_, err := ForkMerge(ctx, s, "wave/job", func(u *types.Job) {
		u.Check, u.State, u.WritePaths = forkCheck(), types.StateDeclared, []string{"run.go", "notes.txt"}
	}, config.Jobs{}, nil)
	require.NoError(t, err)

	_, err = ForkMerge(ctx, s, "wave/job", func(u *types.Job) {
		u.DenyPaths = append(u.DenyPaths, "notes.txt#X")
	}, config.Jobs{}, nil)
	require.ErrorIs(t, err, types.WritePathClaimUngradable)
	assert.Contains(t, err.Error(), `deny path "notes.txt" has no diff driver`)
	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, rows[0].DenyPaths)

	_, err = ForkMerge(ctx, s, "wave/job", func(u *types.Job) {
		u.DenyPaths = append(u.DenyPaths, "run.go#Run")
	}, config.Jobs{}, nil)
	require.NoError(t, err)
}

// A job that writes is held to something wait can grade; one that writes nothing is not.
func TestUngradedProblemsHoldAWritingJobToACheckOrAGoal(t *testing.T) {
	t.Parallel()

	check := types.LeaseCheck{Target: "test", Project: "."}
	goal := types.Goal{ID: "done", Kind: types.GoalKindPaths, Paths: []string{"a.go"}}
	for name, tc := range map[string]struct {
		row     types.Job
		refused bool
	}{
		"a writing job with neither":    {row: types.Job{ID: "w", WritePaths: []string{"a.go"}}, refused: true},
		"a writing job with a check":    {row: types.Job{ID: "w", WritePaths: []string{"a.go"}, Check: &check}},
		"a writing job with a goal":     {row: types.Job{ID: "w", WritePaths: []string{"a.go"}, Goals: []types.Goal{goal}}},
		"a read-only job":               {row: types.Job{ID: "r", ReadOnly: true}},
		"a job declaring no write path": {row: types.Job{ID: "r"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := ungradedProblems(tc.row).Err()
			if !tc.refused {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.JobUngraded)
			assert.Contains(t, err.Error(), `declares neither a check nor a goal`)
			assert.Contains(t, err.Error(), `"goals"`, "the refusal names what to add")
		})
	}

	_, err := ForkMerge(t.Context(), NewStore(tempLocation(t, t.TempDir())), "w",
		func(u *types.Job) { u.WritePaths = []string{"a.go"} }, config.Jobs{}, nil)
	require.ErrorContains(t, err, "declares neither a check nor a goal", "the tool's fork and job.put hold a new row to it")
}

// A forked row is read-only or handed write paths: an empty write set bounds nothing, and
// a check does not stand in for one.
func TestUnscopedProblemsHoldANewRowToABoundary(t *testing.T) {
	t.Parallel()

	check := types.LeaseCheck{Target: "test", Project: "."}
	script := types.LeaseCheck{Script: "probes/callers.buzz"}
	for name, tc := range map[string]struct {
		row     types.Job
		refused bool
	}{
		"no write paths and not read-only": {row: types.Job{ID: "s"}, refused: true},
		"a check and no write paths":       {row: types.Job{ID: "s", Check: &check}, refused: true},
		"a read-only job":                  {row: types.Job{ID: "s", ReadOnly: true}},
		"a read-only job with a script":    {row: types.Job{ID: "s", ReadOnly: true, Check: &script}},
		"a writing job":                    {row: types.Job{ID: "w", WritePaths: []string{"a.go"}, Check: &check}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := unscopedProblems(tc.row).Err()
			if !tc.refused {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.JobUngraded)
			assert.Contains(t, err.Error(), "s names no write paths and is not read-only")
			assert.Contains(t, err.Error(), `{"check": {"script": "<probe>.buzz"}}`, "the refusal names the check a scout passes by")
			assert.Contains(t, err.Error(), "`"+hint.JobExit.With("s")+"`", "and how a scout without one ends")
		})
	}
}

// appliedRow seeds w as a job a worker took, holding write and checked by forkCheck, in a
// root holding each path, the way `magus job apply` finds a live job.
func appliedRow(t *testing.T, write ...string) (*Store, Location) {
	t.Helper()
	root := loadableRepo(t)
	for _, p := range write {
		require.NoError(t, os.WriteFile(filepath.Join(root, p), []byte(p+"\n"), 0o644))
	}
	loc := tempLocation(t, root)
	s := NewStore(loc)
	_, err := s.Update(t.Context(), "w", Declare(spec(write...), 0))
	require.NoError(t, err)
	_, err = s.Exec(t.Context(), "w")
	require.NoError(t, err)
	_, err = s.Update(t.Context(), "w", func(u *types.Job) { u.State = types.StateRunning })
	require.NoError(t, err)
	return s, loc
}

func spec(write ...string) types.Declaration {
	return types.Declaration{ID: "w", Criteria: "goal", WritePaths: write, Check: forkCheck()}
}

func storeBytes(t *testing.T, s *Store) string {
	t.Helper()
	path, err := s.Path()
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}

// Apply upserts the spec and leaves status alone: the holder keeps its job, its state and
// its registration, where a re-fork hands the job out again as declared.
func TestApplyClaimsALiveJobAndKeepsItsState(t *testing.T) {
	t.Parallel()

	s, _ := appliedRow(t, "a.go")
	before, err := s.List()
	require.NoError(t, err)

	got, err := Apply(t.Context(), s, []types.Declaration{spec("a.go", "b.go")}, config.Jobs{}, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.False(t, got[0].Created)
	assert.Equal(t, []string{"write_paths"}, got[0].Changed)

	want := before[0].Clone()
	want.WritePaths = []string{"a.go", "b.go"}
	want.Updated = got[0].Next.Updated
	want.Widenings = 1
	want.WriteProof = types.WriteProofAlone // moved paths are proved again
	want.Generation = before[0].Generation
	assert.NotZero(t, got[0].Next.Validated, "a written record is stamped validated")
	want.Validated = got[0].Next.Validated
	assert.Equal(t, want, got[0].Next)
	assert.Equal(t, types.StateRunning, got[0].Next.State)
}

// The fork derives soft paths and integrator waivers, so a record projected without them
// widens or narrows the row and keeps them; one naming soft_paths for a held path decides.
func TestApplyKeepsDerivedFieldsTheRecordOmits(t *testing.T) {
	t.Parallel()

	waiver := types.JobWaiver{Code: types.OutputOutsideWritePaths, Subject: "gen/a.go", Reason: integratorRegenerates}
	derived := func(t *testing.T) *Store {
		t.Helper()
		root := loadableRepo(t)
		rec := spec("a.go", "a_test.go", "b_test.go")
		rec.SoftPaths, rec.Waivers = []string{"a_test.go", "b_test.go"}, []types.JobWaiver{waiver}
		s := NewStore(tempLocation(t, root))
		_, err := s.Update(t.Context(), "w", Declare(rec, 0))
		require.NoError(t, err)
		_, err = s.Exec(t.Context(), "w")
		require.NoError(t, err)
		_, err = s.Update(t.Context(), "w", func(u *types.Job) { u.State = types.StateRunning })
		require.NoError(t, err)
		return s
	}
	apply := func(t *testing.T, s *Store, rec types.Declaration) types.Job {
		t.Helper()
		got, err := Apply(t.Context(), s, []types.Declaration{rec}, config.Jobs{}, nil, nil, false)
		require.NoError(t, err)
		return got[0].Next
	}

	t.Run("widening", func(t *testing.T) {
		t.Parallel()
		s := derived(t)
		before, err := s.List()
		require.NoError(t, err)
		got := apply(t, s, spec("a.go", "a_test.go", "b_test.go", "c.go"))

		want := before[0].Clone()
		want.WritePaths = []string{"a.go", "a_test.go", "b_test.go", "c.go"}
		want.Updated = got.Updated
		want.Widenings = 1
		want.WriteProof = types.WriteProofAlone
		want.Validated = got.Validated
		assert.Equal(t, want, got, "the soft paths and the waiver stay")
	})
	t.Run("narrowing", func(t *testing.T) {
		t.Parallel()
		got := apply(t, derived(t), spec("a.go", "a_test.go"))
		assert.Equal(t, []string{"a_test.go"}, got.SoftPaths, "a dropped path is no soft path, and the kept one stays soft")
	})
	t.Run("explicit soft paths win", func(t *testing.T) {
		t.Parallel()
		rec := spec("a.go", "a_test.go", "b_test.go")
		rec.SoftPaths = []string{"a_test.go"}
		got := apply(t, derived(t), rec)
		assert.Equal(t, []string{"a_test.go"}, got.SoftPaths)
		assert.Equal(t, []string{"a.go", "b_test.go"}, got.HardPaths())
	})
}

// A worker's own row takes only the release: a spec dropping some of its paths, recorded as
// its own release rather than a revocation. Anything else is refused.
func TestApplyLetsAWorkerOnlyReleaseItsOwnPaths(t *testing.T) {
	t.Parallel()

	s, loc := appliedRow(t, "a.go", "b.go")
	worker := boundStore(loc, "w")

	shrunk, err := Apply(t.Context(), worker, []types.Declaration{spec("a.go")}, config.Jobs{}, nil, nil, false)
	require.NoError(t, err, "a worker may shrink its own row")
	assert.Equal(t, []string{"a.go"}, shrunk[0].Next.WritePaths)
	require.Len(t, shrunk[0].Next.Releases, 1)
	assert.False(t, shrunk[0].Next.Releases[0].Revoked, "giving up its own path is a release, not a revocation")

	var refused *RefusedError
	_, err = Apply(t.Context(), worker, []types.Declaration{spec("a.go", "c.go")}, config.Jobs{}, nil, nil, false)
	require.ErrorAs(t, err, &refused, "widening")
	criteria := spec("a.go")
	criteria.Criteria = "another goal"
	_, err = Apply(t.Context(), worker, []types.Declaration{criteria}, config.Jobs{}, nil, nil, true)
	require.ErrorAs(t, err, &refused, "a dry run answers what the write would")

	rows, err := s.List()
	require.NoError(t, err)
	want := shrunk[0].Next.Clone()
	want.WritePaths, want.Criteria = []string{"a.go"}, "goal"
	assert.Equal(t, want, rows[0], "the refused writes left the shrunk row as it was")
}

func TestApplyDryRunWritesNothing(t *testing.T) {
	t.Parallel()

	s, _ := appliedRow(t, "a.go", "b.go")
	before := storeBytes(t, s)

	got, err := Apply(t.Context(), s, []types.Declaration{spec("b.go")}, config.Jobs{}, nil, nil, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"b.go"}, got[0].Next.WritePaths, "the dry run returns the row the apply would write")
	require.Len(t, got[0].Next.Releases, 1)
	assert.Equal(t, "a.go", got[0].Next.Releases[0].Path)
	assert.True(t, got[0].Next.Releases[0].Revoked, "the orchestrator dropping a holder's path revokes it")
	assert.Equal(t, before, storeBytes(t, s))
}

// The stream is checked whole before any of it is written: a refusal at the second record
// (MGS3018) leaves the first unwritten too.
func TestApplyRefusalLeavesEveryRowUnchanged(t *testing.T) {
	t.Parallel()

	s, _ := appliedRow(t, "a.go")
	before := storeBytes(t, s)
	fresh := types.Declaration{ID: "x", WritePaths: []string{"internal/**"}, Check: forkCheck()}

	_, err := Apply(t.Context(), s, []types.Declaration{spec("a.go", "b.go"), fresh}, config.Jobs{}, nil, nil, false)
	require.ErrorIs(t, err, types.WritePathIsDirectory)
	assert.Equal(t, before, storeBytes(t, s))

	_, err = Apply(t.Context(), s, []types.Declaration{spec("a.go", "internal/**")}, config.Jobs{}, nil, nil, false)
	require.ErrorIs(t, err, types.WritePathIsDirectory, "a path added to a row that exists is held to the fork's rule")
	assert.Equal(t, before, storeBytes(t, s))
}

func TestApplyRefusesStatusAnEndedJobAndAnUnboundedOne(t *testing.T) {
	t.Parallel()

	s, _ := appliedRow(t, "a.go")
	withState := spec("a.go")
	withState.State = types.StatePass
	for name, tc := range map[string]struct {
		rec  types.Declaration
		want string
	}{
		"state":          {rec: withState, want: "which is status"},
		"no write paths": {rec: spec(), want: "`magus job exit w`"},
	} {
		_, err := Apply(t.Context(), s, []types.Declaration{tc.rec}, config.Jobs{}, nil, nil, false)
		require.ErrorContains(t, err, tc.want, name)
	}

	_, err := s.Update(t.Context(), "w", func(u *types.Job) { u.State = types.StateFail })
	require.NoError(t, err)
	_, err = Apply(t.Context(), s, []types.Declaration{spec("a.go")}, config.Jobs{}, nil, nil, false)
	require.ErrorContains(t, err, "w already ended fail")
}

// An integration row that exited red on a file outside its paths took the widening and
// stayed exited, so nobody could take it again; a widened ended row is handed back too.
func TestApplyWideningAnExitedOrEndedRowDeclaresItAgain(t *testing.T) {
	t.Parallel()

	for _, state := range []types.JobState{types.StateExited, types.StateFail} {
		s, _ := appliedRow(t, "a.go")
		_, err := s.Update(t.Context(), "w", func(u *types.Job) { u.State = state })
		require.NoError(t, err)

		got, err := Apply(t.Context(), s, []types.Declaration{spec("a.go", "b.go")}, config.Jobs{}, nil, nil, false)
		require.NoError(t, err, state)
		assert.Equal(t, []string{"a.go", "b.go"}, got[0].Next.WritePaths, state)
		rows, err := s.List()
		require.NoError(t, err)
		assert.Equal(t, types.StateDeclared, rows[0].State, "%s: the stored row is declared, not only the plan", state)
	}

	s, _ := appliedRow(t, "a.go")
	_, err := s.Update(t.Context(), "w", func(u *types.Job) { u.State = types.StateExited })
	require.NoError(t, err)
	kept, err := Apply(t.Context(), s, []types.Declaration{spec("a.go")}, config.Jobs{}, nil, nil, false)
	require.NoError(t, err)
	assert.Equal(t, types.StateExited, kept[0].Next.State, "an exited row changed without widening stays its holder's")
}

// A new id is a fork: declared, with this checkout's checkpoint when the record names none.
func TestApplyCreatesANewJob(t *testing.T) {
	t.Parallel()

	s := NewStore(tempLocation(t, loadableRoot(t)))
	rec := types.Declaration{ID: "fresh", WritePaths: []string{"internal/job/store.go"}, Check: forkCheck()}
	got, err := Apply(t.Context(), s, []types.Declaration{rec}, config.Jobs{}, nil, func() string { return "rev-1" }, false)
	require.NoError(t, err)
	require.True(t, got[0].Created)
	assert.Equal(t, stamped(types.Job{
		ID:         "fresh",
		Checkpoint: "rev-1",
		WritePaths: []string{"internal/job/store.go"},
		Check:      forkCheck(),
		Validation: forkCheck().String(),
		State:      types.StateDeclared,
		WriteProof: got[0].Next.WriteProof, // what the fork could prove is the plan's, not this record's
		Validated:  got[0].Next.Validated,
	}, got[0].Next), got[0].Next)
	assert.NotZero(t, got[0].Next.Validated, "a written record is stamped validated")
}

func TestApplyGradesEveryGoalCheckItAdds(t *testing.T) {
	t.Parallel()

	s, rec := humanOnlyGrader(t)
	lint := types.LeaseCheck{Target: "lint", Project: "."}
	bench := types.LeaseCheck{Target: "bench", Project: "."}
	_, err := ForkMerge(t.Context(), s, "w", func(u *types.Job) { u.WritePaths, u.Check = []string{"pkg/b.go"}, &lint }, config.Jobs{}, nil)
	require.NoError(t, err)
	graded := len(rec.graded)
	before := storeBytes(t, s)

	paid := types.Declaration{ID: "w", WritePaths: []string{"pkg/b.go"}, Check: &lint, Goals: []types.Goal{
		{ID: "paid", Kind: types.GoalKindCheck, Expect: types.ExpectPassed, Check: bench},
	}}
	for _, dryRun := range []bool{true, false} {
		_, err = Apply(t.Context(), s, []types.Declaration{paid}, config.Jobs{}, nil, nil, dryRun)
		require.ErrorContains(t, err, "w's goal paid's check `magus run bench .` is a target a person runs", "dry run %v", dryRun)
		assert.Equal(t, before, storeBytes(t, s), "a refused apply writes nothing, dry run %v", dryRun)
	}

	_, err = ForkMerge(t.Context(), s, "w", func(u *types.Job) {
		u.Goals = append(u.Goals, types.Goal{ID: "paid", Kind: types.GoalKindCheck, Expect: types.ExpectPassed, Check: bench})
	}, config.Jobs{}, nil)
	require.ErrorContains(t, err, "w's goal paid's check", "a merge that adds a goal check is graded too")

	rec.graded = rec.graded[:graded]
	again := paid
	again.Goals = []types.Goal{{ID: "again", Kind: types.GoalKindCheck, Expect: types.ExpectPassed, Check: lint}}
	_, err = Apply(t.Context(), s, []types.Declaration{again}, config.Jobs{}, nil, nil, false)
	require.NoError(t, err)
	assert.Len(t, rec.graded, graded, "a goal naming a check the row already holds is not graded again")
}

// Every door that forks by merge pins the rows it read for the guard's grading, creating a
// row or updating one, so grading a check never sweeps the store a second time.
func TestForkDoorsGradeAgainstTheRowsTheyRead(t *testing.T) {
	t.Parallel()

	var pinned [][]string
	loc := tempLocation(t, testRoot(t).Root())
	loc.Grader = func(ctx context.Context, _ string, row types.Job) CheckVerdict {
		snap, ok := SnapshotFromContext(ctx)
		if !assert.True(t, ok, "%s was graded with no rows pinned", row.ID) {
			return CheckVerdict{}
		}
		ids := make([]string, len(snap.Rows))
		for i, r := range snap.Rows {
			ids[i] = r.ID
		}
		pinned = append(pinned, ids)
		return CheckVerdict{}
	}
	s := NewStore(loc)
	lint := &types.LeaseCheck{Target: "lint", Project: "."}

	_, err := ForkMerge(t.Context(), s, "a", func(u *types.Job) { u.WritePaths, u.Check = []string{"pkg/a.go"}, forkCheck() }, config.Jobs{}, nil)
	require.NoError(t, err)
	_, err = Apply(t.Context(), s, []types.Declaration{{ID: "b", WritePaths: []string{"pkg/b.go"}, Check: forkCheck()}}, config.Jobs{}, nil, nil, false)
	require.NoError(t, err)
	_, err = ForkMerge(t.Context(), s, "a", func(u *types.Job) { u.Check = lint }, config.Jobs{}, nil)
	require.NoError(t, err)
	_, err = Apply(t.Context(), s, []types.Declaration{{ID: "b", WritePaths: []string{"pkg/b.go"}, Check: lint}}, config.Jobs{}, nil, nil, false)
	require.NoError(t, err)

	assert.Equal(t, [][]string{{}, {"a"}, {"a", "b"}, {"a", "b"}}, pinned)
}
