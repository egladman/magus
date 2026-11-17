package job

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/tier"
)

type jobSuite struct{ suite.Suite }

func TestJob(t *testing.T) { suite.Run(t, new(jobSuite)) }

func (s *jobSuite) refusesID(id, rule string) {
	s.T().Helper()
	var refused *idError
	s.Require().ErrorAs(validateID(id), &refused, id)
	s.Equal(rule, refused.Rule, id)
	s.False(ValidID(id), id)
}

func (s *jobSuite) TestValidateIDNamesTheRuleAnIDBreaks() {
	s.Require().NoError(validateID("area/fix-parser.v2"))
	s.refusesID("", "an id is not empty")
	s.refusesID("a..b", "an id never contains ..")
	s.refusesID("a:b", "an id never contains ':'")
	s.refusesID("Fix", "an id is lowercase letters, digits, '-', '.' and '/' between segments")
	s.refusesID("a//b", "'/' goes between segments: an id neither starts nor ends with it, nor doubles it")
	s.refusesID("area/.git", "no segment of an id starts with '.' or '-'")
}

func (s *jobSuite) TestAnIDIsAtMostMaxIDLen() {
	s.Require().NoError(validateID(strings.Repeat("a", MaxIDLen)))
	s.refusesID(strings.Repeat("a", MaxIDLen+1), fmt.Sprintf("an id is at most %d characters", MaxIDLen))
}

func (s *jobSuite) TestParseLeaseCheckRefusesWithACheckError() {
	c, err := ParseLeaseCheck("test cmd/magus/ -- -run X")
	s.Require().NoError(err)
	s.Equal(LeaseCheck{Target: "test", Project: "cmd/magus", Args: []string{"-run", "X"}}, c)
	c, err = ParseLeaseCheck("generate --no-default-charms")
	s.Require().NoError(err)
	s.Equal(LeaseCheck{Target: "generate", Project: ".", NoDefaultCharms: true}, c)
	s.refusesCheck("")
	s.refusesCheck("-o json test .")
	s.refusesCheck("test . extra")
}

func (s *jobSuite) refusesCheck(line string) {
	s.T().Helper()
	_, err := ParseLeaseCheck(line)
	var refused *CheckError
	s.Require().ErrorAs(err, &refused, line)
	s.Equal(line, refused.Line)
}

func (s *jobSuite) TestATimeoutIsEmptyOrAPositiveDuration() {
	d, err := ParseTimeout(" ")
	s.Require().NoError(err)
	s.Zero(d)
	d, err = ParseTimeout("45m")
	s.Require().NoError(err)
	s.Equal(45*time.Minute, d)
	_, err = ParseTimeout("-1h")
	var refused *TimeoutError
	s.Require().ErrorAs(err, &refused)
	s.Equal("-1h", refused.Timeout)
}

func (s *jobSuite) TestAClaimEvidenceRefusalIsTyped() {
	_, err := ParseClaimEvidence("hunch:x")
	var refused *ClaimEvidenceError
	s.Require().ErrorAs(err, &refused)
	s.Equal("hunch:x", refused.Because)
}

func (s *jobSuite) TestAGoalResolvesItsKindAndExpectation() {
	s.Equal(Goal{ID: "g", Kind: GoalKindCheck, Expect: ExpectPassed}, Goal{ID: "g"}.Resolve())
	s.Equal(Goal{ID: "g", Kind: GoalKindPaths, Expect: ExpectChanged}, Goal{ID: "g", Kind: GoalKindPaths}.Resolve())
	s.Equal(ExpectAbsent, Goal{Kind: GoalKindSymbol, Expect: ExpectAbsent}.Resolve().Expect)
}

func (s *jobSuite) TestACheckGoalsSubjectIsTheCheckAsParseLeaseCheckReadsIt() {
	s.Equal([]string{"test cmd/magus -- -run X"},
		Goal{Kind: GoalKindCheck, Check: LeaseCheck{Target: "test", Project: "cmd/magus", Args: []string{"-run", "X"}}}.Subject())
	s.Equal([]string{"probes/key.buzz"}, Goal{Kind: GoalKindCheck, Check: LeaseCheck{Script: "probes/key.buzz", Test: true}}.Subject())
	s.Equal([]string{"lint ."}, Goal{Kind: GoalKindCheck, Check: LeaseCheck{Target: "lint"}}.Subject())
	s.Nil(Goal{Kind: GoalKindCheck}.Subject())
}

func (s *jobSuite) TestEffectiveGoalsLeadWithThePrimaryCheck() {
	row := Job{Check: &LeaseCheck{Target: "test", Project: "."}, Goals: []Goal{{ID: "docs", Kind: GoalKindPaths, Paths: []string{"docs"}}}}
	goals := row.EffectiveGoals()
	s.Require().Len(goals, 2)
	s.Equal(PrimaryGoalID, goals[0].ID)
	s.Equal(ExpectPassed, goals[0].Expect)
	s.Equal([]Basis{BasisWitnessed}, goals[0].Accept, "the lease's own check is run by its holder's runner")
	s.Equal(ExpectChanged, goals[1].Expect, "the declared goal comes back resolved")
	s.Len(Job{}.EffectiveGoals(), 0)
}

func (s *jobSuite) TestStatesSortLiveEditingAndTerminal() {
	s.True(StateExited.Live())
	s.False(StateExited.Editing(), "an exited job holds its checkout and claims no paths")
	s.True(StateRunning.Editing())
	s.True(StateNoReturn.Terminal())
	s.False(State("").Live(), "a job with no state has not said it is live")
	s.False(ValidState(""))
}

func (s *jobSuite) TestCloneSharesNothing() {
	row := Job{
		Paths:     Paths{Write: []string{"a"}},
		Check:     &LeaseCheck{Target: "test", Args: []string{"-run", "X"}},
		ModelTier: &tier.Assignment{Name: "routine", Shared: []string{"x"}},
		Attempt:   &CapturedRun{ExtraArgs: []string{"-v"}},
		Rejection: &Rejection{Violations: []string{"v"}},
	}
	c := row.Clone()
	c.Paths.Write[0] = "b"
	c.Check.Args[0] = "-count"
	c.Check.Target = "lint"
	c.ModelTier.Shared[0] = "y"
	c.Attempt.ExtraArgs[0] = "-race"
	c.Rejection.Violations[0] = "w"
	s.Equal("a", row.Paths.Write[0])
	s.Equal(LeaseCheck{Target: "test", Args: []string{"-run", "X"}}, *row.Check)
	s.Equal("x", row.ModelTier.Shared[0])
	s.Equal("-v", row.Attempt.ExtraArgs[0])
	s.Equal("v", row.Rejection.Violations[0])
	s.Nil(Job{}.Clone().Paths.Write, "nil stays nil")
}

func (s *jobSuite) TestSpecChangedSinceTreatsZeroAsUnknown() {
	row := Job{Generation: 3}
	s.True(row.SpecChangedSince(2))
	s.False(row.SpecChangedSince(3))
	s.False(row.SpecChangedSince(0))
	s.False(Job{}.SpecChangedSince(2))
}

func (s *jobSuite) TestAQueuedJobIsNeverStale() {
	dep := Job{ID: "dep", State: StateRunning, Updated: 10}
	queued := Job{ID: "q", State: StateDeclared, DependsOn: []string{"dep"}, Updated: 10}
	s.False(StaleAt([]Job{dep, queued}, queued, 10_000, time.Minute))
	dep.State = StatePass
	dep.Updated = 50
	since, waiting := IdleSince([]Job{dep, queued}, queued)
	s.False(waiting)
	s.Equal(int64(50), since, "the clock starts at the dependency's last update")
	s.True(StaleAt([]Job{dep, queued}, queued, 50+60, time.Minute))
	s.False(StaleAt([]Job{dep, queued}, queued, 50+60, 0), "a zero window flags nothing")
}

func (s *jobSuite) TestAJobBlockedOnADependencyClaimsNoPaths() {
	a := Job{ID: "a", State: StateRunning, Paths: Paths{Write: []string{"internal/job"}}}
	b := Job{ID: "b", State: StateRunning, Paths: Paths{Write: []string{"internal/job/store.go"}}, DependsOn: []string{"c"}}
	list := NewList([]Job{a, b})
	s.Empty(list.Overlaps)
	s.Equal([]Block{{Job: "b", On: "c"}}, list.Blocked)
	s.Equal("blocked on c which is undeclared", list.Blocked[0].String())
	b.DependsOn = nil
	s.Equal([]Overlap{{JobA: "a", JobB: "b", PathsA: []string{"internal/job"}, PathsB: []string{"internal/job/store.go"}}},
		NewList([]Job{a, b}).Overlaps)
}

func (s *jobSuite) TestAnEmptyListServesNoJobsRatherThanNull() {
	s.Equal([]Job{}, NewList(nil).Jobs)
}

func (s *jobSuite) TestFlagNamesOverdueOrphanedAndStaleJobs() {
	parent := Job{ID: "p", State: StatePass, Updated: 0}
	child := Job{ID: "p/c", Parent: "p", State: StateRunning, Deadline: 5, Updated: 0}
	list := NewList([]Job{parent, child}).Flag(10, time.Second)
	s.Equal([]string{"p/c"}, list.Overdue)
	s.Equal([]string{"p/c"}, list.Orphans)
	s.Equal([]string{"p/c"}, list.Stale)
}

func (s *jobSuite) TestPathsIntersectByPrefixAndClaim() {
	s.True(PathsIntersect("internal/job", "internal/job/store.go"))
	s.True(PathsIntersect("**/*.go", "docs/a.md"), "a pattern with no literal prefix claims the tree")
	s.False(PathsIntersect("internal/guard/*_test.go", "internal/guard/guard.go"))
	s.False(PathsIntersect("run.go#A", "run.go#B"))
	s.True(PathsIntersect("run.go#A", "run.go"))
	s.False(PathsIntersect("", "run.go"), "an entry naming nothing claims nothing")
}

func (s *jobSuite) TestPathWithinOnlyNarrows() {
	s.True(PathWithin("internal/job/store.go", "internal/job"))
	s.True(PathWithin("internal/job/*_test.go", "internal/**/*.go"))
	s.True(PathWithin("run.go#A", "run.go"))
	s.False(PathWithin("run.go", "run.go#A"))
	s.False(PathWithin("internal/**", "internal/*.go"), "two globs of another shape are never within")
}

func (s *jobSuite) TestAnIntegrationBranchIsOneGitTakes() {
	s.True(validIntegratedBranch("job/fix-parser"))
	s.False(validIntegratedBranch(""))
	s.False(validIntegratedBranch("-x"), "git would read it as an option")
	s.False(validIntegratedBranch("a..b"))
	s.False(validIntegratedBranch("a.lock"))
	s.False(validIntegratedBranch("a/"))
	s.False(validIntegratedBranch("a@{1}"))
	s.False(validIntegratedBranch("a b"))
	s.False(validIntegratedBranch("a~1"))
}

func (s *jobSuite) TestAServerRunHasNoAgentAndTheServersOrigin() {
	s.True(Job{RegisteredBy: Origin{EntryPoint: EntryPointServer}}.ServerRun())
	s.False(Job{RegisteredBy: Origin{EntryPoint: EntryPointServer}, Agent: Instructed("x")}.ServerRun())
}

func (s *jobSuite) TestAReadOnlyJobWithNoCheckHoldsNoBuildSeat() {
	s.True(Job{ReadOnly: true}.HoldsNoBuildSeat())
	s.False(Job{ReadOnly: true, Check: &LeaseCheck{Target: "test"}}.HoldsNoBuildSeat())
	s.False(Job{}.HoldsNoBuildSeat())
}

func (s *jobSuite) TestApplyLeavesValidationToTheStore() {
	row := Job{Validation: "kept"}
	Declaration{ID: "a", Check: &LeaseCheck{Target: "test", Project: "cmd/magus/"}}.Apply(&row)
	s.Equal(&LeaseCheck{Target: "test", Project: "cmd/magus"}, row.Check)
	s.Equal("kept", row.Validation)
}

const declared = `{"api_version":"safetyscissors.job/v1alpha1","id":"a","later":{"x":[1]}}`

func (s *jobSuite) TestADeclarationKeepsUnknownMembersThroughEncodingJSON() {
	var d Declaration
	s.Require().NoError(json.Unmarshal([]byte(declared), &d))
	s.Equal("a", d.ID)
	s.Equal(map[string]codec.RawMessage{"later": codec.RawMessage(`{"x":[1]}`)}, d.Unknown)
	out, err := json.Marshal(d)
	s.Require().NoError(err)
	s.Contains(string(out), `"later":{"x":[1]}`)
	var back Declaration
	s.Require().NoError(codec.Unmarshal(out, &back))
	s.Equal(d.Unknown, back.Unknown)
}

func (s *jobSuite) TestAStrictDecodeRefusesAnAuthoredDeclarationsUnknownMember() {
	var d Declaration
	s.Require().ErrorIs(codec.UnmarshalStrict([]byte(declared), &d), codec.ErrUnknownField)
	s.Require().ErrorIs(codec.UnmarshalStrict([]byte(`{"api_version":"safetyscissors.job/v1alpha1","id":"a","check":{"target":"t","x":1}}`), &d),
		codec.ErrUnknownField, "and a nested one")
	s.Require().NoError(codec.UnmarshalStrict([]byte(`{"api_version":"safetyscissors.job/v1alpha1","id":"a"}`), &d))
	s.Nil(d.Unknown)
}

func (s *jobSuite) TestAResultKeepsUnknownMembersToo() {
	var r Result
	s.Require().NoError(json.Unmarshal([]byte(`{"api_version":"v","job":"a","changed_paths":[],"unresolved_risks":[],"confidence":"high"}`), &r))
	s.Equal(map[string]codec.RawMessage{"confidence": codec.RawMessage(`"high"`)}, r.Unknown)
	s.Require().ErrorIs(codec.UnmarshalStrict([]byte(`{"api_version":"v","confidence":"high"}`), &r), codec.ErrUnknownField)
}

func (s *jobSuite) faultsAccepted(g Goal) []string {
	var out []string
	for _, f := range g.Faults() {
		if strings.HasPrefix(f.Field, "accept") {
			out = append(out, f.Field+": "+f.Message)
		}
	}
	return out
}

func (s *jobSuite) TestACheckGoalAcceptingNoBasisIsAFault() {
	g := Goal{ID: "tests", Check: LeaseCheck{Target: "test", Project: "."}}

	s.Equal([]string{`accept: goal "tests" is a check goal accepting no basis, so no run could grade it`}, s.faultsAccepted(g))
	s.Empty(g.Resolve().Accept, "resolving names no basis on the goal's behalf")
}

func (s *jobSuite) TestAnUnknownOrRepeatedBasisIsAFault() {
	g := Goal{ID: "tests", Check: LeaseCheck{Target: "test", Project: "."}, Accept: []Basis{BasisWitnessed, "trusted", BasisWitnessed}}

	s.Equal([]string{
		`accept[1]: goal "tests" accepts "trusted", which is not a basis`,
		`accept[2]: goal "tests" accepts "witnessed" twice`,
	}, s.faultsAccepted(g))
}

func (s *jobSuite) TestACheckGoalNamingItsBasesHasNoAcceptFault() {
	g := Goal{ID: "tests", Check: LeaseCheck{Target: "test", Project: "."}, Accept: []Basis{BasisWitnessed, BasisReported}}

	s.Empty(s.faultsAccepted(g))
}

func (s *jobSuite) TestOnlyACheckGoalCarriesAccept() {
	g := Goal{ID: "docs", Kind: GoalKindPaths, Paths: []string{"docs"}, Accept: []Basis{BasisReported}}

	s.Equal([]string{`accept: goal "docs" is a paths goal and carries accept, which only a check goal reads`}, s.faultsAccepted(g))
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
		"at the length cap":   {strings.Repeat("u", MaxIDLen), true},
		"past the length cap": {strings.Repeat("u", MaxIDLen+1), false},
		"empty":               {"", false},
		"space":               {"two words", false},
		"punctuation":         {"MGS1021!", false},
		"newline":             {"lease\n", false},
		"non-ascii":           {"lease-é", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ValidID(tc.id))
		})
	}
}

func owner(id string, state State, paths ...string) Job {
	return Job{ID: id, State: state, Paths: Paths{Write: paths}}
}

func TestJobOverlaps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		leases []Job
		want   []Overlap
	}{
		{
			name:   "the same path claimed twice",
			leases: []Job{owner("a", StateRunning, "internal/ledger"), owner("b", StateDeclared, "internal/ledger")},
			want: []Overlap{
				{JobA: "a", JobB: "b", PathsA: []string{"internal/ledger"}, PathsB: []string{"internal/ledger"}},
			},
		},
		{
			// The common case a reader most needs told: one lease owns the directory, the
			// other owns a file inside it, and nothing about either row says so.
			name:   "a file inside a claimed directory, and both declarations are named",
			leases: []Job{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledger/store.go")},
			want: []Overlap{
				{
					JobA: "a", JobB: "b",
					PathsA: []string{"internal/ledger"}, PathsB: []string{"internal/ledger/store.go"},
				},
			},
		},
		{
			name:   "a glob is judged by the directories it names",
			leases: []Job{owner("a", StateRunning, "console/src/**/*.ts"), owner("b", StateRunning, "console/src/console/plan/main.ts")},
			want: []Overlap{
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
			want: []Overlap{
				{JobA: "a", JobB: "b", PathsA: []string{"internal"}, PathsB: []string{"internal/ledger"}},
				{JobA: "a", JobB: "c", PathsA: []string{"internal"}, PathsB: []string{"internal/handler"}},
			},
		},
		{
			// Listed rather than dropped: the file is shared, and "disjoint" is what tells the
			// reader the pair is an integration order and not a wait.
			name:   "different declarations of one file are a disjoint pair",
			leases: []Job{owner("a", StateRunning, "run.go#executeStages"), owner("b", StateRunning, "run.go#RunCI", "docs/x.md")},
			want: []Overlap{
				{JobA: "a", JobB: "b", PathsA: []string{"run.go#executeStages"}, PathsB: []string{"run.go#RunCI"}, Claims: ClaimsDisjoint},
			},
		},
		{
			name:   "one declaration claimed twice is shared",
			leases: []Job{owner("a", StateRunning, "run.go#RunCI"), owner("b", StateRunning, "run.go# RunCI ")},
			want: []Overlap{
				{JobA: "a", JobB: "b", PathsA: []string{"run.go#RunCI"}, PathsB: []string{"run.go# RunCI "}, Claims: ClaimsShared},
			},
		},
		{
			name:   "a declaration inside a directory claimed whole is shared",
			leases: []Job{owner("a", StateRunning, "internal/job"), owner("b", StateRunning, "internal/job/store.go#digest")},
			want: []Overlap{
				{JobA: "a", JobB: "b", PathsA: []string{"internal/job"}, PathsB: []string{"internal/job/store.go#digest"}, Claims: ClaimsShared},
			},
		},
		{
			name:   "a disjoint file beside a shared one makes the pair shared",
			leases: []Job{owner("a", StateRunning, "run.go#A", "b.go"), owner("b", StateRunning, "run.go#B", "b.go")},
			want: []Overlap{
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

			assert.Equal(t, tt.want, NewList(tt.leases).Overlaps)
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
	for _, state := range []State{StatePass, StateFail, StateNoReturn} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			done := owner("done", state, "internal/ledger")
			assert.Empty(t, NewList([]Job{live, done}).Overlaps)
			assert.Empty(t, NewList([]Job{done, live}).Overlaps, "whichever order the rows sit in")
		})
	}
}

// TestPathHoldersKeepsEditingRows: a path is held by a declared or running row only. An
// exited row filed its result, so its outputs are anyone's to regenerate.
func TestPathHoldersKeepsEditingRows(t *testing.T) {
	t.Parallel()

	rows := []Job{
		owner("declared", StateDeclared, "a"),
		owner("running", StateRunning, "b"),
		owner("exited", StateExited, "c"),
		owner("passed", StatePass, "d"),
		owner("unset", "", "e"),
	}
	var ids []string
	for _, r := range PathHolders(rows) {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"declared", "running"}, ids)
	assert.Len(t, rows, 5, "the rows passed in are left whole")
}

// An exited holder returned, so its write paths collide with nobody's while it waits to be
// graded.
func TestJobOverlapsSkipsAnExitedJob(t *testing.T) {
	t.Parallel()

	live := owner("live", StateRunning, "internal/ledger")
	exited := owner("exited", StateExited, "internal/ledger")
	assert.Empty(t, NewList([]Job{live, exited}).Overlaps)
	assert.Empty(t, NewList([]Job{exited, live}).Overlaps, "whichever order the rows sit in")
	assert.True(t, StateExited.Live(), "an exited job stays live until it is graded")
	assert.False(t, StateExited.Editing())
}

func TestNewJobListDerivesOverlapsFromTheRows(t *testing.T) {
	t.Parallel()

	leases := []Job{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledger")}
	report := NewList(leases)
	assert.Equal(t, leases, report.Jobs, "the rows are served exactly as they were recorded")
	assert.Len(t, report.Overlaps, 1)

	// Derived on read: the same rows with one of them finished report nothing, and no
	// row had to be rewritten for that to happen.
	leases[1].State = StatePass
	assert.Empty(t, NewList(leases).Overlaps)
}

// The empty case is settled in the constructor rather than at each read door, so the MCP
// tool and the console's route cannot disagree about it: an unwritten ledger serves an
// empty list, never null, whichever door served it.
func TestNewJobListNormalizesTheEmptyLedger(t *testing.T) {
	t.Parallel()

	report := NewList(nil)
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
		ID: "a",
		Paths: Paths{
			Write: append(make([]string, 0, 4), "types/"),
			Deny:  append(make([]string, 0, 4), "gen/"),
		},
		DependsOn:    append(make([]string, 0, 4), "b"),
		Breaks:       append(make([]string, 0, 4), "--old-flag"),
		Goals:        append(make([]Goal, 0, 4), Goal{ID: "unit", Check: LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"check"}}),
		GateAttempts: append(make([]GateAttempt, 0, 4), GateAttempt{GateID: "unit", Attempt: CapturedRun{Ref: "out1"}}),
		Releases:     append(make([]Release, 0, 4), Release{Path: "types/x.go"}),
		Unattributed: append(make([]UnattributedWrite, 0, 4), UnattributedWrite{Path: "types/y.go"}),
	}

	first, second := orig.Clone(), orig.Clone()
	first.Paths.Write = append(first.Paths.Write, "first/")
	first.Paths.Deny = append(first.Paths.Deny, "first/")
	first.DependsOn = append(first.DependsOn, "first")
	first.Breaks = append(first.Breaks, "first")
	first.Goals[0].DependsOn = append(first.Goals[0].DependsOn, "first")
	first.GateAttempts = append(first.GateAttempts, GateAttempt{GateID: "first"})
	first.Releases = append(first.Releases, Release{Path: "first/z.go"})
	first.Unattributed = append(first.Unattributed, UnattributedWrite{Path: "first/z.go"})

	second.Paths.Write = append(second.Paths.Write, "second/")
	second.Paths.Deny = append(second.Paths.Deny, "second/")
	second.DependsOn = append(second.DependsOn, "second")
	second.Breaks = append(second.Breaks, "second")
	second.Goals[0].DependsOn = append(second.Goals[0].DependsOn, "second")
	second.GateAttempts = append(second.GateAttempts, GateAttempt{GateID: "second"})
	second.Releases = append(second.Releases, Release{Path: "second/z.go"})
	second.Unattributed = append(second.Unattributed, UnattributedWrite{Path: "second/z.go"})

	// Clone resolves each gate's kind and expectation, so the clone's gate carries both.
	assert.Equal(t, Job{
		ID: "a",
		Paths: Paths{
			Write: []string{"types/", "first/"},
			Deny:  []string{"gen/", "first/"},
		},
		DependsOn:    []string{"b", "first"},
		Breaks:       []string{"--old-flag", "first"},
		Goals:        []Goal{{ID: "unit", Kind: GoalKindCheck, Expect: ExpectPassed, Check: LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"check", "first"}}},
		GateAttempts: []GateAttempt{{GateID: "unit", Attempt: CapturedRun{Ref: "out1"}}, {GateID: "first"}},
		Releases:     []Release{{Path: "types/x.go"}, {Path: "first/z.go"}},
		Unattributed: []UnattributedWrite{{Path: "types/y.go"}, {Path: "first/z.go"}},
	}, first)

	// The original is the store's row and nobody appended through it, so it must still
	// hold exactly what it held.
	assert.Equal(t, Job{
		ID: "a",
		Paths: Paths{
			Write: []string{"types/"},
			Deny:  []string{"gen/"},
		},
		DependsOn:    []string{"b"},
		Breaks:       []string{"--old-flag"},
		Goals:        []Goal{{ID: "unit", Check: LeaseCheck{Target: "test", Project: "."}, DependsOn: []string{"check"}}},
		GateAttempts: []GateAttempt{{GateID: "unit", Attempt: CapturedRun{Ref: "out1"}}},
		Releases:     []Release{{Path: "types/x.go"}},
		Unattributed: []UnattributedWrite{{Path: "types/y.go"}},
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
		Attempt:      &CapturedRun{Ref: "out1", ExtraArgs: args()},
		GateAttempts: []GateAttempt{{GateID: "unit", Attempt: CapturedRun{Ref: "out2", ExtraArgs: args()}}},
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
	unmet := func() []VerificationStatus {
		return append(make([]VerificationStatus, 0, 4), VerificationStatus{ID: "unit", Violations: refs()})
	}
	orig := Job{ID: "a", Rejection: &Rejection{Unmet: unmet(), Violations: refs()}}

	first, second := orig.Clone(), orig.Clone()
	for i, c := range []Job{first, second} {
		tag := fmt.Sprint(i)
		c.Rejection.Unmet[0].Violations = append(c.Rejection.Unmet[0].Violations, tag)
		c.Rejection.Violations = append(c.Rejection.Violations, tag)
	}
	first.Rejection.At = 9

	assert.Equal(t, []string{"out1", "1"}, second.Rejection.Unmet[0].Violations)
	assert.Equal(t, []string{"out1", "1"}, second.Rejection.Violations)
	assert.Equal(t, Job{ID: "a", Rejection: &Rejection{Unmet: unmet(), Violations: refs()}}, orig)
}

// The generation and the rejection are the store's, so a declaration
// never writes them: Apply leaves them where they were.
func TestDeclarationApplyLeavesStoreComputedFieldsAlone(t *testing.T) {
	t.Parallel()

	row := Job{
		ID:         "worker",
		Generation: 2,
		Rejection:  &Rejection{At: 1, ObservedGeneration: 2},
	}
	want := row
	want.Agent = Instructed("again")
	Declaration{ID: "worker", Agent: Instructed("again")}.Apply(&row)
	assert.Equal(t, want, row)
}

func TestJobSpecChangedSinceReadsAZeroStampAsUnknown(t *testing.T) {
	t.Parallel()

	assert.False(t, Job{Generation: 2}.SpecChangedSince(2), "same generation")
	assert.True(t, Job{Generation: 2}.SpecChangedSince(1), "an earlier spec")
	assert.False(t, Job{Generation: 2}.SpecChangedSince(0), "an unstamped record")
	assert.False(t, Job{}.SpecChangedSince(1), "a row with no generation")
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

func TestJobUnmetNamesWhatThisBuildLacks(t *testing.T) {
	t.Parallel()

	assert.Nil(t, Job{}.Unmet())
	assert.Nil(t, Job{Requires: []string{"claim-declarations"}}.Unmet())
	assert.Equal(t, []string{"jobs-v99", "x"}, Job{Requires: []string{"jobs-v99", "signature-goals", "x"}}.Unmet())
}

func TestServerRunNeedsNoAgentAndTheServerAsRegistrar(t *testing.T) {
	t.Parallel()

	server := Origin{EntryPoint: EntryPointServer}
	assert.True(t, Job{RegisteredBy: server}.ServerRun())
	assert.False(t, Job{RegisteredBy: server, Agent: &Agent{}}.ServerRun(), "an agent section is a session's work")
	assert.False(t, Job{RegisteredBy: Origin{EntryPoint: EntryPointCLI}}.ServerRun())
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

// An exited dependency is done editing, so nobody collecting it leaves its dependent
// queued forever: the clock starts at the exit, and only a declared or running one holds it.
func TestJobIdleSinceStartsAtAnExitedDependency(t *testing.T) {
	t.Parallel()

	row := Job{ID: "next", State: StateDeclared, Updated: 100, DependsOn: []string{"first"}}
	exited := Job{ID: "first", State: StateExited, Updated: 300}
	since, waiting := IdleSince([]Job{exited, row}, row)
	assert.False(t, waiting)
	assert.Equal(t, int64(300), since)
	assert.True(t, StaleAt([]Job{exited, row}, row, 300+3600, time.Hour))

	for _, state := range []State{StateRunning, StateDeclared} {
		dep := Job{ID: "first", State: state, Updated: 300}
		_, waiting = IdleSince([]Job{dep, row}, row)
		assert.True(t, waiting, "a %s dependency still holds the clock", state)
	}
}

func dependent(id string, state State, dependsOn string, paths ...string) Job {
	return Job{ID: id, State: state, Paths: Paths{Write: paths}, DependsOn: []string{dependsOn}}
}

func TestJobOverlapsOmitsADependentThatIsNotReady(t *testing.T) {
	t.Parallel()

	for _, state := range []State{StateDeclared, StateRunning, StateExited} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			rows := []Job{owner("dep", state, "internal/job"), dependent("waiter", StateDeclared, "dep", "internal/job")}
			report := NewList(rows)
			assert.Empty(t, report.Overlaps, "a job waiting on dep claims nothing dep could collide with")
			assert.Equal(t, []Block{{Job: "waiter", On: "dep", State: state}}, report.Blocked)
		})
	}

	t.Run("a third live job is not shielded by the waiter", func(t *testing.T) {
		t.Parallel()

		rows := []Job{
			owner("dep", StateRunning, "types"),
			dependent("waiter", StateDeclared, "dep", "internal/job"),
			owner("other", StateRunning, "internal/job"),
		}
		assert.Empty(t, NewList(rows).Overlaps)
	})
}

func TestJobOverlapsReportsADependentOnceEveryDependencyPasses(t *testing.T) {
	t.Parallel()

	rows := []Job{
		owner("dep", StateRunning, "types"),
		{ID: "waiter", State: StateDeclared, Paths: Paths{Write: []string{"internal/job"}}, DependsOn: []string{"dep", "dep2"}},
		owner("dep2", StatePass),
		owner("other", StateRunning, "internal/job"),
	}
	assert.Empty(t, NewList(rows).Overlaps, "one dependency still running keeps the waiter out")

	rows[0].State = StatePass
	report := NewList(rows)
	assert.Equal(t, []Overlap{{JobA: "waiter", JobB: "other", PathsA: []string{"internal/job"}, PathsB: []string{"internal/job"}}}, report.Overlaps)
	assert.Empty(t, report.Blocked)
}

func TestJobListNamesWhyAJobOwnsNothing(t *testing.T) {
	t.Parallel()

	for _, state := range []State{StateFail, StateNoReturn} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			rows := []Job{owner("dep", state), dependent("waiter", StateDeclared, "dep", "internal/job"), owner("other", StateRunning, "internal/job")}
			report := NewList(rows)
			assert.Empty(t, report.Overlaps)
			assert.Equal(t, []Block{{Job: "waiter", On: "dep", State: state}}, report.Blocked)
		})
	}

	t.Run("an undeclared dependency", func(t *testing.T) {
		t.Parallel()

		report := NewList([]Job{dependent("waiter", StateDeclared, "gone", "internal/job"), owner("other", StateRunning, "internal/job")})
		assert.Empty(t, report.Overlaps)
		assert.Equal(t, []Block{{Job: "waiter", On: "gone"}}, report.Blocked)
	})

	t.Run("a terminal dependent is not reported", func(t *testing.T) {
		t.Parallel()

		assert.Empty(t, NewList([]Job{owner("dep", StateFail), dependent("waiter", StateNoReturn, "dep")}).Blocked)
	})

	t.Run("json names the reason", func(t *testing.T) {
		t.Parallel()

		got, err := codec.Marshal(NewList([]Job{owner("dep", StateFail), dependent("waiter", StateDeclared, "dep")}))
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
	assert.Equal(t, []string{"lone"}, NewList(rows).Flag(now, 2*time.Hour).Stale, "a live dependency keeps its waiter queued")

	rows[0].State, rows[0].Updated = StatePass, now-hour
	assert.Equal(t, []string{"lone"}, NewList(rows).Flag(now, 2*time.Hour).Stale, "the waiter's clock starts at its dependency's end")
	assert.Equal(t, []string{"queued", "lone"}, NewList(rows).Flag(now+2*hour, 2*time.Hour).Stale)
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
		for _, a := range Ancestors(rows, id) {
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
	allocs := testing.AllocsPerRun(10, func() { Ancestors(rows, "leaf") })
	assert.LessOrEqual(t, allocs, 4.0, "only the returned chain allocates, whatever the store's size")
}

// A footprint that is not known still says so on the wire: footprint_known is never
// omitted, so a reader cannot take a missing footprint for an empty one.
func TestFootprintsCrossJSON(t *testing.T) {
	t.Parallel()

	status, err := codec.Marshal(Status{Job: "a", FootprintReason: "git does not report changed regions (RegionReporter)"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"job":"a","verified":false,"footprint_known":false,"footprint_reason":"git does not report changed regions (RegionReporter)","lines_added":0,"lines_deleted":0,"symbol_delta_known":false}`, string(status))

	status, err = codec.Marshal(Status{Job: "a", FootprintKnown: true, Footprint: []RegionChange{
		{File: FileChange{Path: "a.go"}, Side: RegionNew, Lines: [2]int{1, 4}, Declaration: "func X() {", Driver: "golang"},
	}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"job":"a","verified":false,"footprint_known":true,"footprint":[`+
		`{"file":{"path":"a.go"},"side":"new","lines":[1,4],"declaration":"func X() {","driver":"golang"}],"lines_added":0,"lines_deleted":0,"symbol_delta_known":false}`, string(status))

	status, err = codec.Marshal(Status{Job: "a", ObservedGeneration: 2, PriorGeneration: 1})
	require.NoError(t, err)
	assert.JSONEq(t, `{"job":"a","verified":false,"footprint_known":false,"lines_added":0,"lines_deleted":0,"symbol_delta_known":false,`+
		`"observed_generation":2,"prior_generation":1}`, string(status))

	overlap, err := codec.Marshal(Overlap{JobA: "a", JobB: "b", PathsA: []string{"x"}, PathsB: []string{"x"},
		Footprint: &OverlapFootprint{Verdict: FootprintShared, Shared: []string{"x/a.go#func X() {"}}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"job_a":"a","job_b":"b","paths_a":["x"],"paths_b":["x"],`+
		`"footprint":{"verdict":"shared","shared":["x/a.go#func X() {"]}}`, string(overlap))
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

// A pause holds until its bound and not a second past it; a pause naming no bound holds
// until it is lifted.
func TestJobPausedAtLapsesAtItsBound(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		row  Job
		want bool
	}{
		"not paused":       {row: Job{PausedUntil: 200}, want: false},
		"paused, no bound": {row: Job{Paused: 10, PauseReason: "busy"}, want: true},
		"inside its bound": {row: Job{Paused: 10, PauseReason: "busy", PausedUntil: 101}, want: true},
		"at its bound":     {row: Job{Paused: 10, PauseReason: "busy", PausedUntil: 100}, want: false},
	}
	for name, c := range cases {
		assert.Equal(t, c.want, c.row.PausedAt(100), name)
	}
}

func TestParseClaimEvidenceReadsADeadHolder(t *testing.T) {
	t.Parallel()

	e, err := ParseClaimEvidence("dead:api/migrate")
	require.NoError(t, err)
	assert.Equal(t, ClaimEvidence{Kind: ClaimDeadHolder, Subject: "api/migrate"}, e)
	assert.Equal(t, "dead:api/migrate", e.String())

	_, err = ParseClaimEvidence("dead")
	assert.EqualError(t, err, `"dead" names no dead subject`)
}

func TestHoldsNoBuildSeat(t *testing.T) {
	check := Goal{Kind: GoalKindCheck, Check: LeaseCheck{Target: "test", Project: "."}}
	assert.True(t, Job{ReadOnly: true}.HoldsNoBuildSeat())
	assert.False(t, Job{ReadOnly: true, Goals: []Goal{check}}.HoldsNoBuildSeat(), "a check runs a target")
	assert.False(t, Job{ReadOnly: true, Paths: Paths{Write: []string{"a/**"}}}.HoldsNoBuildSeat())
	assert.False(t, Job{}.HoldsNoBuildSeat(), "a writer is not read-only")
}
