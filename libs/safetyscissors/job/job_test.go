package job

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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

func (s *jobSuite) TestAPostconditionResolvesItsKindAndExpectation() {
	s.Equal(Postcondition{ID: "g", Kind: PostconditionKindCheck, Expect: ExpectPassed}, Postcondition{ID: "g"}.Resolve())
	s.Equal(Postcondition{ID: "g", Kind: PostconditionKindPaths, Expect: ExpectChanged}, Postcondition{ID: "g", Kind: PostconditionKindPaths}.Resolve())
	s.Equal(ExpectAbsent, Postcondition{Kind: PostconditionKindSymbol, Expect: ExpectAbsent}.Resolve().Expect)
}

// Only the store's CheckSpec reads a check, so a check postcondition's subject is none here.
func (s *jobSuite) TestACheckPostconditionHasNoSubject() {
	s.Nil(Postcondition{Kind: PostconditionKindCheck, Check: checkJSON("test", ".")}.Subject())
	s.Equal([]string{"docs"}, Postcondition{Kind: PostconditionKindPaths, Paths: []string{"docs"}}.Subject())
}

func (s *jobSuite) TestEffectivePostconditionsLeadWithThePrimaryCheck() {
	row := Job{Check: checkJSON("test", "."), Postconditions: []Postcondition{{ID: "docs", Kind: PostconditionKindPaths, Paths: []string{"docs"}}}}
	postconditions := row.EffectivePostconditions()
	s.Require().Len(postconditions, 2)
	s.Equal(PrimaryPostconditionID, postconditions[0].ID)
	s.Equal(row.Check, postconditions[0].Check)
	s.Empty(Job{Check: rawCheck("null")}.EffectivePostconditions(), "a null check declares none")
	s.Equal(ExpectPassed, postconditions[0].Expect)
	s.Equal([]Basis{BasisWitnessed}, postconditions[0].Accept, "the lease's own check is run by its holder's runner")
	s.Equal(ExpectChanged, postconditions[1].Expect, "the declared postcondition comes back resolved")
	s.Len(Job{}.EffectivePostconditions(), 0)
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
		Check:     checkJSON("test", "."),
		ModelTier: &tier.Assignment{Name: "routine", Shared: []string{"x"}},
		Attempt:   &CapturedRun{ExtraArgs: []string{"-v"}},
		Rejection: &Rejection{Violations: []string{"v"}},
	}
	c := row.Clone()
	c.Paths.Write[0] = "b"
	c.ModelTier.Shared[0] = "y"
	c.Attempt.ExtraArgs[0] = "-race"
	c.Rejection.Violations[0] = "w"
	s.Equal("a", row.Paths.Write[0])
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
	s.False(Job{ReadOnly: true, Check: checkJSON("test", ".")}.HoldsNoBuildSeat())
	s.False(Job{}.HoldsNoBuildSeat())
}

// Apply copies the check as declared: what it says is the store's CheckSpec's to read.
func (s *jobSuite) TestApplyLeavesTheCheckAndValidationToTheStore() {
	row := Job{Validation: "kept"}
	check := rawCheck(`{"target":"test","project":"cmd/magus/"}`)
	Declaration{ID: "a", Check: check}.Apply(&row)
	s.Equal(check, row.Check)
	s.Equal("kept", row.Validation)
	Declaration{ID: "a"}.Apply(&row)
	s.True(row.Check.IsZero())
}

const declared = `{"api_version":"safetyscissors.job/v1alpha1","id":"a","later":{"x":[1]}}`

func (s *jobSuite) TestADeclarationKeepsUnknownMembersThroughEncodingJSON() {
	var d Declaration
	s.Require().NoError(json.Unmarshal([]byte(declared), &d))
	s.Equal("a", d.ID)
	s.Equal([]string{"later"}, d.Unknown())
	out, err := json.Marshal(d)
	s.Require().NoError(err)
	s.Contains(string(out), `"later":{"x":[1]}`)
	var back Declaration
	s.Require().NoError(codec.Unmarshal(out, &back))
	s.Equal(d, back)
}

func (s *jobSuite) TestAStrictDecodeRefusesAnAuthoredDeclarationsUnknownMember() {
	var d Declaration
	s.Require().ErrorIs(codec.UnmarshalStrict([]byte(declared), &d), codec.ErrUnknownField)
	s.Require().NoError(codec.UnmarshalStrict([]byte(`{"api_version":"safetyscissors.job/v1alpha1","id":"a","check":{"target":"t","x":1}}`), &d),
		"a check is opaque here: the store's CheckSpec refuses what it does not declare")
	s.JSONEq(`{"target":"t","x":1}`, d.Check.String())
	s.Require().NoError(codec.UnmarshalStrict([]byte(`{"api_version":"safetyscissors.job/v1alpha1","id":"a"}`), &d))
	s.Nil(d.Unknown())
}

func (s *jobSuite) TestAResultKeepsUnknownMembersToo() {
	var r Result
	s.Require().NoError(json.Unmarshal([]byte(`{"api_version":"v","job":"a","changed_paths":[],"unresolved_risks":[],"confidence":"high"}`), &r))
	s.Equal([]string{"confidence"}, r.Unknown())
	out, err := codec.Marshal(r)
	s.Require().NoError(err)
	s.Contains(string(out), `"confidence":"high"`)
	s.Require().ErrorIs(codec.UnmarshalStrict([]byte(`{"api_version":"v","confidence":"high"}`), &r), codec.ErrUnknownField)
}

func (s *jobSuite) TestAStoreHeaderKeepsUnknownMembersAndAddsOneOnACopy() {
	var h Rows
	s.Require().NoError(json.Unmarshal([]byte(`{"api_version":"v","retained":3,"later":{"x":[1]}}`), &h))
	s.Equal([]string{"later"}, h.Unknown())
	stamped := h.WithMember("writer", codec.RawMessage(`"w"`))
	s.Equal([]string{"later", "writer"}, stamped.Unknown())
	s.Equal([]string{"later"}, h.Unknown(), "WithMember derives a copy")
	s.Equal([]string{"later", "writer"}, stamped.Header().Unknown())
	out, err := codec.Marshal(stamped)
	s.Require().NoError(err)
	s.Contains(string(out), `"later":{"x":[1]}`)
	s.Contains(string(out), `"writer":"w"`)
	s.Nil(Rows{}.Unknown())
}

func (s *jobSuite) faultsAccepted(g Postcondition) []string {
	var out []string
	for _, f := range g.Faults() {
		if strings.HasPrefix(f.Field, "accept") {
			out = append(out, f.Field+": "+f.Message)
		}
	}
	return out
}

func (s *jobSuite) TestACheckPostconditionAcceptingNoBasisIsAFault() {
	g := Postcondition{ID: "tests", Check: checkJSON("test", ".")}

	s.Equal([]string{`accept: postcondition "tests" is a check postcondition accepting no basis, so no run could grade it`}, s.faultsAccepted(g))
	s.Empty(g.Resolve().Accept, "resolving names no basis on the postcondition's behalf")
}

func (s *jobSuite) TestAnUnknownOrRepeatedBasisIsAFault() {
	g := Postcondition{ID: "tests", Check: checkJSON("test", "."), Accept: []Basis{BasisWitnessed, "trusted", BasisWitnessed}}

	s.Equal([]string{
		`accept[1]: postcondition "tests" accepts "trusted", which is not a basis`,
		`accept[2]: postcondition "tests" accepts "witnessed" twice`,
	}, s.faultsAccepted(g))
}

func (s *jobSuite) TestACheckPostconditionNamingItsBasesHasNoAcceptFault() {
	g := Postcondition{ID: "tests", Check: checkJSON("test", "."), Accept: []Basis{BasisWitnessed, BasisReported}}

	s.Empty(s.faultsAccepted(g))
}

func (s *jobSuite) TestOnlyACheckPostconditionCarriesAccept() {
	g := Postcondition{ID: "docs", Kind: PostconditionKindPaths, Paths: []string{"docs"}, Accept: []Basis{BasisReported}}

	s.Equal([]string{`accept: postcondition "docs" is a paths postcondition and carries accept, which only a check postcondition reads`}, s.faultsAccepted(g))
}

// ValidID is the rule every lease channel shares. The marker scanner is not the
// only producer: whatever stamps a Job has to agree with this, or internal/trail's
// redaction exemption starts covering strings nobody checked.
func (s *jobSuite) TestValidIDAcceptsTheShapesEveryLeaseChannelShares() {
	s.True(ValidID("mgs1021"), "plain")
	s.True(ValidID("a.b-1/2"), "every separator")
	s.True(ValidID("feat/spawn-capture"), "branch shaped")
	s.True(ValidID(strings.Repeat("u", MaxIDLen)), "at the length cap")
}

func (s *jobSuite) TestValidIDRefusesWhatBreaksARule() {
	s.False(ValidID("MGS1021"), "uppercase")
	s.False(ValidID("a:b"), "colon")
	s.False(ValidID("a_b"), "underscore")
	s.False(ValidID("a..b"), "double dot")
	s.False(ValidID("/a"), "leading slash")
	s.False(ValidID("a/"), "trailing slash")
	s.False(ValidID("a//b"), "doubled slash")
	s.False(ValidID("a/.b"), "segment dot start")
	s.False(ValidID("a/-b"), "segment dash start")
	s.False(ValidID("-a"), "leading dash")
	s.False(ValidID(strings.Repeat("u", MaxIDLen+1)), "past the length cap")
	s.False(ValidID(""), "empty")
	s.False(ValidID("two words"), "space")
	s.False(ValidID("MGS1021!"), "punctuation")
	s.False(ValidID("lease\n"), "newline")
	s.False(ValidID("lease-é"), "non-ascii")
}

func owner(id string, state State, paths ...string) Job {
	return Job{ID: id, State: state, Paths: Paths{Write: paths}}
}

func (s *jobSuite) TestSpecChangedSinceReadsAZeroStampAsUnknown() {
	s.False(Job{Generation: 2}.SpecChangedSince(2), "same generation")
	s.True(Job{Generation: 2}.SpecChangedSince(1), "an earlier spec")
	s.False(Job{Generation: 2}.SpecChangedSince(0), "an unstamped record")
	s.False(Job{}.SpecChangedSince(1), "a row with no generation")
}

func (s *jobSuite) TestUnmetNamesWhatThisBuildLacks() {
	s.Nil(Job{}.Unmet())
	s.Nil(Job{Requires: []string{"claim-declarations"}}.Unmet())
	s.Equal([]string{"jobs-v99", "x"}, Job{Requires: []string{"jobs-v99", "signature-postconditions", "x"}}.Unmet())
}

func (s *jobSuite) TestServerRunNeedsNoAgentAndTheServerAsRegistrar() {
	server := Origin{EntryPoint: EntryPointServer}
	s.True(Job{RegisteredBy: server}.ServerRun())
	s.False(Job{RegisteredBy: server, Agent: &Agent{}}.ServerRun(), "an agent section is a session's work")
	s.False(Job{RegisteredBy: Origin{EntryPoint: EntryPointCLI}}.ServerRun())
}

func (s *jobSuite) TestHoldsNoBuildSeatNeedsNoCheckNoWritePathsAndAReadOnlyRow() {
	check := Postcondition{Kind: PostconditionKindCheck, Check: checkJSON("test", ".")}
	s.True(Job{ReadOnly: true}.HoldsNoBuildSeat())
	s.False(Job{ReadOnly: true, Postconditions: []Postcondition{check}}.HoldsNoBuildSeat(), "a check runs a target")
	s.False(Job{ReadOnly: true, Paths: Paths{Write: []string{"a/**"}}}.HoldsNoBuildSeat())
	s.False(Job{}.HoldsNoBuildSeat(), "a writer is not read-only")
}

// The generation and the rejection are the store's, so a declaration
// never writes them: Apply leaves them where they were.
func (s *jobSuite) TestDeclarationApplyLeavesStoreComputedFieldsAlone() {
	row := Job{
		ID:         "worker",
		Generation: 2,
		Rejection:  &Rejection{At: 1, ObservedGeneration: 2},
	}
	want := row
	want.Agent = Instructed("again")
	Declaration{ID: "worker", Agent: Instructed("again")}.Apply(&row)
	s.Equal(want, row)
}

// The record is the whole spec, so applying one that omits breaks clears them: a job
// keeps the right to break something only while its declaration says so.
func (s *jobSuite) TestDeclarationApplyCopiesBreaksAndClearsAnOmittedOne() {
	var row Job
	Declaration{ID: "worker", Breaks: []string{" --old-flag ", "", "api.Open"}}.Apply(&row)
	s.Equal([]string{"--old-flag", "api.Open"}, row.Breaks)

	Declaration{ID: "worker"}.ApplySpec(&row)
	s.Empty(row.Breaks)
}

func (s *jobSuite) TestDeclarationApplyCopiesCreates() {
	var row Job
	Declaration{ID: "worker", Creates: []string{" internal/hostcall ", ""}}.Apply(&row)
	s.Equal([]string{"internal/hostcall"}, row.Creates)

	Declaration{ID: "worker"}.ApplySpec(&row)
	s.Empty(row.Creates)
}

func (s *jobSuite) TestACreatesEntryNamesOneWorkspaceDirectory() {
	d := Declaration{ID: "worker", Creates: []string{"internal/hostcall", "libs/new/", "", "internal/*", "/abs", "../out", ".", "a//b"}}

	var fields []string
	for _, f := range d.Faults() {
		if strings.HasPrefix(f.Field, "creates") {
			fields = append(fields, f.Field)
		}
	}
	s.Equal([]string{"creates[2]", "creates[3]", "creates[4]", "creates[5]", "creates[6]", "creates[7]"}, fields)
}

func (s *jobSuite) TestNewDirsLinesSayUnknownNeverNothing() {
	s.Equal([]string{"new directories: unknown, no graph"}, NewDirs{Unknown: "no graph"}.Lines())
	s.Empty(NewDirs{}.Lines())
	s.Equal([]string{"new directory internal/a: no dir node; directories sharing its words: none"}, NewDirs{Dirs: []NewDir{{Dir: "internal/a"}}}.Lines())
}

// A pause holds until its bound and not a second past it; a pause naming no bound holds
// until it is lifted.
func (s *jobSuite) TestPausedAtLapsesAtItsBound() {
	s.False(Job{PausedUntil: 200}.PausedAt(100), "not paused")
	s.True(Job{Paused: 10, PauseReason: "busy"}.PausedAt(100), "paused, no bound")
	s.True(Job{Paused: 10, PauseReason: "busy", PausedUntil: 101}.PausedAt(100), "inside its bound")
	s.False(Job{Paused: 10, PauseReason: "busy", PausedUntil: 100}.PausedAt(100), "at its bound")
}

// Ancestors walks nearest first, stops at a parent the rows do not carry and at a
// cycle, and allocates only the chain it returns: every list asks it of every live row,
// and a per-call index of the whole store made one list quadratic in allocations.
func (s *jobSuite) TestAncestors() {
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
	s.Equal([]string{"mid", "root"}, ids("leaf"))
	s.Empty(ids("root"))
	s.Empty(ids("orphan"), "a parent the rows do not carry ends the walk")
	s.Equal([]string{"b"}, ids("a"), "a cycle back to the start ends the walk")
	s.Equal([]string{"a", "b"}, ids("into"), "a cycle above the start ends the walk")

	for i := range 1000 {
		rows = append(rows, Job{ID: fmt.Sprintf("filler-%d", i)})
	}
	allocs := testing.AllocsPerRun(10, func() { Ancestors(rows, "leaf") })
	s.LessOrEqual(allocs, 4.0, "only the returned chain allocates, whatever the store's size")
}

// A footprint that is not known still says so on the wire: footprint_known is never
// omitted, so a reader cannot take a missing footprint for an empty one.
func (s *jobSuite) TestFootprintsCrossJSON() {
	status, err := codec.Marshal(Status{Job: "a", FootprintReason: "git does not report changed regions (RegionReporter)"})
	s.Require().NoError(err)
	s.JSONEq(`{"job":"a","verified":false,"footprint_known":false,"footprint_reason":"git does not report changed regions (RegionReporter)","lines_added":0,"lines_deleted":0,"symbol_delta_known":false}`, string(status))

	status, err = codec.Marshal(Status{Job: "a", FootprintKnown: true, Footprint: []RegionChange{
		{File: FileChange{Path: "a.go"}, Side: RegionNew, Lines: [2]int{1, 4}, Declaration: "func X() {", Driver: "golang"},
	}})
	s.Require().NoError(err)
	s.JSONEq(`{"job":"a","verified":false,"footprint_known":true,"footprint":[`+
		`{"file":{"path":"a.go"},"side":"new","lines":[1,4],"declaration":"func X() {","driver":"golang"}],"lines_added":0,"lines_deleted":0,"symbol_delta_known":false}`, string(status))

	status, err = codec.Marshal(Status{Job: "a", ObservedGeneration: 2, PriorGeneration: 1})
	s.Require().NoError(err)
	s.JSONEq(`{"job":"a","verified":false,"footprint_known":false,"lines_added":0,"lines_deleted":0,"symbol_delta_known":false,`+
		`"observed_generation":2,"prior_generation":1}`, string(status))

	overlap, err := codec.Marshal(Overlap{JobA: "a", JobB: "b", PathsA: []string{"x"}, PathsB: []string{"x"},
		Footprint: &OverlapFootprint{Verdict: FootprintShared, Shared: []string{"x/a.go#func X() {"}}})
	s.Require().NoError(err)
	s.JSONEq(`{"job_a":"a","job_b":"b","paths_a":["x"],"paths_b":["x"],`+
		`"footprint":{"verdict":"shared","shared":["x/a.go#func X() {"]}}`, string(overlap))
}

type ListSuite struct{ suite.Suite }

func TestListSuite(t *testing.T) { suite.Run(t, new(ListSuite)) }

func (s *ListSuite) TestTheSamePathClaimedTwice() {
	leases := []Job{owner("a", StateRunning, "internal/ledger"), owner("b", StateDeclared, "internal/ledger")}

	s.Equal([]Overlap{
		{JobA: "a", JobB: "b", PathsA: []string{"internal/ledger"}, PathsB: []string{"internal/ledger"}},
	}, NewList(leases).Overlaps)
}

// The common case a reader most needs told: one lease owns the directory, the
// other owns a file inside it, and nothing about either row says so.
func (s *ListSuite) TestAFileInsideAClaimedDirectoryNamesBothDeclarations() {
	leases := []Job{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledger/store.go")}

	s.Equal([]Overlap{
		{
			JobA: "a", JobB: "b",
			PathsA: []string{"internal/ledger"}, PathsB: []string{"internal/ledger/store.go"},
		},
	}, NewList(leases).Overlaps)
}

func (s *ListSuite) TestAGlobIsJudgedByTheDirectoriesItNames() {
	leases := []Job{owner("a", StateRunning, "console/src/**/*.ts"), owner("b", StateRunning, "console/src/console/plan/main.ts")}

	s.Equal([]Overlap{
		{
			JobA: "a", JobB: "b",
			PathsA: []string{"console/src/**/*.ts"},
			PathsB: []string{"console/src/console/plan/main.ts"},
		},
	}, NewList(leases).Overlaps)
}

func (s *ListSuite) TestSiblingDirectoriesAreNotAnOverlap() {
	leases := []Job{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledgerx", "console/")}

	s.Nil(NewList(leases).Overlaps)
}

// A prefix that stops mid-segment is a different directory, not a parent.
func (s *ListSuite) TestASharedNamePrefixInsideASegmentIsNotContainment() {
	leases := []Job{owner("a", StateRunning, "internal/led"), owner("b", StateRunning, "internal/ledger/store.go")}

	s.Nil(NewList(leases).Overlaps)
}

// A blank entry names nothing. It cleans to ".", and reading THAT as a claim on
// the whole tree paired the row holding it with every other lease in the plan.
func (s *ListSuite) TestABlankDeclarationClaimsNothingNotEverything() {
	leases := []Job{owner("a", StateRunning, "  ", ""), owner("b", StateRunning, "internal/ledger")}

	s.Nil(NewList(leases).Overlaps)
}

func (s *ListSuite) TestAReadOnlyLeaseDeclaresNoPathsSoItCollidesWithNothing() {
	leases := []Job{owner("a", StateRunning, "internal/ledger"), {ID: "scout", State: StateRunning, ReadOnly: true}}

	s.Nil(NewList(leases).Overlaps)
}

// The pair is reported once, and the ids read in ledger order so a reader can
// find both rows in the table they are looking at.
func (s *ListSuite) TestThreeLeasesClaimingOneTreeAreThreePairsEachNamedOnce() {
	leases := []Job{
		owner("a", StateRunning, "internal"),
		owner("b", StateRunning, "internal/ledger"),
		owner("c", StateDeclared, "internal/handler"),
	}

	s.Equal([]Overlap{
		{JobA: "a", JobB: "b", PathsA: []string{"internal"}, PathsB: []string{"internal/ledger"}},
		{JobA: "a", JobB: "c", PathsA: []string{"internal"}, PathsB: []string{"internal/handler"}},
	}, NewList(leases).Overlaps)
}

// Listed rather than dropped: the file is shared, and "disjoint" is what tells the
// reader the pair is an integration order and not a wait.
func (s *ListSuite) TestDifferentDeclarationsOfOneFileAreADisjointPair() {
	leases := []Job{owner("a", StateRunning, "run.go#executeStages"), owner("b", StateRunning, "run.go#RunCI", "docs/x.md")}

	s.Equal([]Overlap{
		{JobA: "a", JobB: "b", PathsA: []string{"run.go#executeStages"}, PathsB: []string{"run.go#RunCI"}, Claims: ClaimsDisjoint},
	}, NewList(leases).Overlaps)
}

func (s *ListSuite) TestOneDeclarationClaimedTwiceIsShared() {
	leases := []Job{owner("a", StateRunning, "run.go#RunCI"), owner("b", StateRunning, "run.go# RunCI ")}

	s.Equal([]Overlap{
		{JobA: "a", JobB: "b", PathsA: []string{"run.go#RunCI"}, PathsB: []string{"run.go# RunCI "}, Claims: ClaimsShared},
	}, NewList(leases).Overlaps)
}

func (s *ListSuite) TestADeclarationInsideADirectoryClaimedWholeIsShared() {
	leases := []Job{owner("a", StateRunning, "internal/job"), owner("b", StateRunning, "internal/job/store.go#digest")}

	s.Equal([]Overlap{
		{JobA: "a", JobB: "b", PathsA: []string{"internal/job"}, PathsB: []string{"internal/job/store.go#digest"}, Claims: ClaimsShared},
	}, NewList(leases).Overlaps)
}

func (s *ListSuite) TestADisjointFileBesideASharedOneMakesThePairShared() {
	leases := []Job{owner("a", StateRunning, "run.go#A", "b.go"), owner("b", StateRunning, "run.go#B", "b.go")}

	s.Equal([]Overlap{
		{JobA: "a", JobB: "b", PathsA: []string{"run.go#A", "b.go"}, PathsB: []string{"run.go#B", "b.go"}, Claims: ClaimsShared},
	}, NewList(leases).Overlaps)
}

func (s *ListSuite) TestDeclarationsOfDifferentFilesDoNotMeetAtAll() {
	leases := []Job{owner("a", StateRunning, "run.go#A"), owner("b", StateRunning, "order.go#A")}

	s.Nil(NewList(leases).Overlaps)
}

// A finished lease is not competing for anything. The skill has a worker RELEASE its
// paths when it stops editing, so reporting a pass, a fail, or a no-return as a
// collision would make the report loudest exactly as the plan winds down.
func (s *ListSuite) finishedLeaseCollidesWithNothing(state State) {
	s.T().Helper()
	live := owner("live", StateRunning, "internal/ledger")
	done := owner("done", state, "internal/ledger")
	s.Empty(NewList([]Job{live, done}).Overlaps, string(state))
	s.Empty(NewList([]Job{done, live}).Overlaps, "%s, whichever order the rows sit in", state)
}

func (s *ListSuite) TestAPassedLeaseIsNotACollision() {
	s.finishedLeaseCollidesWithNothing(StatePass)
}

func (s *ListSuite) TestAFailedLeaseIsNotACollision() {
	s.finishedLeaseCollidesWithNothing(StateFail)
}

func (s *ListSuite) TestANoReturnLeaseIsNotACollision() {
	s.finishedLeaseCollidesWithNothing(StateNoReturn)
}

// PathHolders: a path is held by a declared or running row only. An
// exited row filed its result, so its outputs are anyone's to regenerate.
func (s *ListSuite) TestPathHoldersKeepsEditingRows() {
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
	s.Equal([]string{"declared", "running"}, ids)
	s.Len(rows, 5, "the rows passed in are left whole")
}

// An exited holder returned, so its write paths collide with nobody's while it waits to be
// graded.
func (s *ListSuite) TestAnExitedJobIsNotACollision() {
	live := owner("live", StateRunning, "internal/ledger")
	exited := owner("exited", StateExited, "internal/ledger")
	s.Empty(NewList([]Job{live, exited}).Overlaps)
	s.Empty(NewList([]Job{exited, live}).Overlaps, "whichever order the rows sit in")
	s.True(StateExited.Live(), "an exited job stays live until it is graded")
	s.False(StateExited.Editing())
}

func (s *ListSuite) TestNewListDerivesOverlapsFromTheRows() {
	leases := []Job{owner("a", StateRunning, "internal/ledger"), owner("b", StateRunning, "internal/ledger")}
	report := NewList(leases)
	s.Equal(leases, report.Jobs, "the rows are served exactly as they were recorded")
	s.Len(report.Overlaps, 1)

	// Derived on read: the same rows with one of them finished report nothing, and no
	// row had to be rewritten for that to happen.
	leases[1].State = StatePass
	s.Empty(NewList(leases).Overlaps)
}

// The empty case is settled in the constructor rather than at each read door, so the MCP
// tool and the console's route cannot disagree about it: an unwritten ledger serves an
// empty list, never null, whichever door served it.
func (s *ListSuite) TestNewListNormalizesTheEmptyLedger() {
	report := NewList(nil)
	s.NotNil(report.Jobs, "a workspace where nobody has declared a lease yet is empty, not broken")
	s.Empty(report.Jobs)
	s.Empty(report.Overlaps)
}

// An exited dependency is done editing, so nobody collecting it leaves its dependent
// queued forever: the clock starts at the exit, and only a declared or running one holds it.
func (s *ListSuite) TestIdleSinceStartsAtAnExitedDependency() {
	row := Job{ID: "next", State: StateDeclared, Updated: 100, DependsOn: []string{"first"}}
	exited := Job{ID: "first", State: StateExited, Updated: 300}
	since, waiting := IdleSince([]Job{exited, row}, row)
	s.False(waiting)
	s.Equal(int64(300), since)
	s.True(StaleAt([]Job{exited, row}, row, 300+3600, time.Hour))

	for _, state := range []State{StateRunning, StateDeclared} {
		dep := Job{ID: "first", State: state, Updated: 300}
		_, waiting = IdleSince([]Job{dep, row}, row)
		s.True(waiting, "a %s dependency still holds the clock", state)
	}
}

func dependent(id string, state State, dependsOn string, paths ...string) Job {
	return Job{ID: id, State: state, Paths: Paths{Write: paths}, DependsOn: []string{dependsOn}}
}

func (s *ListSuite) waiterBlockedOn(state State) {
	s.T().Helper()
	rows := []Job{owner("dep", state, "internal/job"), dependent("waiter", StateDeclared, "dep", "internal/job")}
	report := NewList(rows)
	s.Empty(report.Overlaps, "a job waiting on dep claims nothing dep could collide with")
	s.Equal([]Block{{Job: "waiter", On: "dep", State: state}}, report.Blocked)
}

func (s *ListSuite) TestOverlapsOmitsAWaiterOnADeclaredDependency() {
	s.waiterBlockedOn(StateDeclared)
}

func (s *ListSuite) TestOverlapsOmitsAWaiterOnARunningDependency() {
	s.waiterBlockedOn(StateRunning)
}

func (s *ListSuite) TestOverlapsOmitsAWaiterOnAnExitedDependency() {
	s.waiterBlockedOn(StateExited)
}

func (s *ListSuite) TestAThirdLiveJobIsNotShieldedByTheWaiter() {
	rows := []Job{
		owner("dep", StateRunning, "types"),
		dependent("waiter", StateDeclared, "dep", "internal/job"),
		owner("other", StateRunning, "internal/job"),
	}
	s.Empty(NewList(rows).Overlaps)
}

func (s *ListSuite) TestOverlapsReportsADependentOnceEveryDependencyPasses() {
	rows := []Job{
		owner("dep", StateRunning, "types"),
		{ID: "waiter", State: StateDeclared, Paths: Paths{Write: []string{"internal/job"}}, DependsOn: []string{"dep", "dep2"}},
		owner("dep2", StatePass),
		owner("other", StateRunning, "internal/job"),
	}
	s.Empty(NewList(rows).Overlaps, "one dependency still running keeps the waiter out")

	rows[0].State = StatePass
	report := NewList(rows)
	s.Equal([]Overlap{{JobA: "waiter", JobB: "other", PathsA: []string{"internal/job"}, PathsB: []string{"internal/job"}}}, report.Overlaps)
	s.Empty(report.Blocked)
}

func (s *ListSuite) ownsNothingBehindEndedDependency(state State) {
	s.T().Helper()
	rows := []Job{owner("dep", state), dependent("waiter", StateDeclared, "dep", "internal/job"), owner("other", StateRunning, "internal/job")}
	report := NewList(rows)
	s.Empty(report.Overlaps)
	s.Equal([]Block{{Job: "waiter", On: "dep", State: state}}, report.Blocked)
}

func (s *ListSuite) TestAJobBehindAFailedDependencyOwnsNothing() {
	s.ownsNothingBehindEndedDependency(StateFail)
}

func (s *ListSuite) TestAJobBehindANoReturnDependencyOwnsNothing() {
	s.ownsNothingBehindEndedDependency(StateNoReturn)
}

func (s *ListSuite) TestAnUndeclaredDependencyIsNamedAsTheBlock() {
	report := NewList([]Job{dependent("waiter", StateDeclared, "gone", "internal/job"), owner("other", StateRunning, "internal/job")})
	s.Empty(report.Overlaps)
	s.Equal([]Block{{Job: "waiter", On: "gone"}}, report.Blocked)
}

func (s *ListSuite) TestATerminalDependentIsNotReportedBlocked() {
	s.Empty(NewList([]Job{owner("dep", StateFail), dependent("waiter", StateNoReturn, "dep")}).Blocked)
}

func (s *ListSuite) TestBlockedJSONNamesTheReason() {
	got, err := codec.Marshal(NewList([]Job{owner("dep", StateFail), dependent("waiter", StateDeclared, "dep")}))
	s.Require().NoError(err)
	s.Contains(string(got), `"blocked":[{"job":"waiter","on":"dep","state":"fail"}]`)
}

func (s *ListSuite) TestFlagsAQueuedJobStaleOnlyOnceItsDependenciesEnd() {
	const now, hour = int64(100_000), int64(3600)
	rows := []Job{
		{ID: "dep", State: StateRunning, Registered: now, Updated: now},
		{ID: "queued", State: StateDeclared, DependsOn: []string{"dep"}, Updated: now - 3*hour},
		{ID: "lone", State: StateDeclared, Updated: now - 3*hour},
	}
	s.Equal([]string{"lone"}, NewList(rows).Flag(now, 2*time.Hour).Stale, "a live dependency keeps its waiter queued")

	rows[0].State, rows[0].Updated = StatePass, now-hour
	s.Equal([]string{"lone"}, NewList(rows).Flag(now, 2*time.Hour).Stale, "the waiter's clock starts at its dependency's end")
	s.Equal([]string{"queued", "lone"}, NewList(rows).Flag(now+2*hour, 2*time.Hour).Stale)
}

type CloneSuite struct{ suite.Suite }

func TestCloneSuite(t *testing.T) { suite.Run(t, new(CloneSuite)) }

// Clone is what makes a row handed out of a store safe to keep, so a slice field it
// forgets aliases the store's own array. Every field here carries SPARE CAPACITY, which is
// the shape the ledger actually stores (RecordUnattributedWrite sizes its slice for one
// more than it fills): an append then lands in place, and the damage is invisible to the
// appender: it shows up as the NEXT reader's append overwriting the first one's row. So
// this asserts across two clones rather than back at the original, which is the only form
// of the check that can fail.
func (s *CloneSuite) TestCopiesEverySliceField() {
	orig := Job{
		ID: "a",
		Paths: Paths{
			Write: append(make([]string, 0, 4), "types/"),
			Deny:  append(make([]string, 0, 4), "gen/"),
		},
		DependsOn:             append(make([]string, 0, 4), "b"),
		Breaks:                append(make([]string, 0, 4), "--old-flag"),
		Postconditions:        append(make([]Postcondition, 0, 4), Postcondition{ID: "unit", Check: checkJSON("test", "."), DependsOn: []string{"check"}}),
		PostconditionAttempts: append(make([]PostconditionAttempt, 0, 4), PostconditionAttempt{PostconditionID: "unit", Attempt: CapturedRun{Ref: "out1"}}),
		Releases:              append(make([]Release, 0, 4), Release{Path: "types/x.go"}),
		Unattributed:          append(make([]UnattributedWrite, 0, 4), UnattributedWrite{Path: "types/y.go"}),
	}

	first, second := orig.Clone(), orig.Clone()
	first.Paths.Write = append(first.Paths.Write, "first/")
	first.Paths.Deny = append(first.Paths.Deny, "first/")
	first.DependsOn = append(first.DependsOn, "first")
	first.Breaks = append(first.Breaks, "first")
	first.Postconditions[0].DependsOn = append(first.Postconditions[0].DependsOn, "first")
	first.PostconditionAttempts = append(first.PostconditionAttempts, PostconditionAttempt{PostconditionID: "first"})
	first.Releases = append(first.Releases, Release{Path: "first/z.go"})
	first.Unattributed = append(first.Unattributed, UnattributedWrite{Path: "first/z.go"})

	second.Paths.Write = append(second.Paths.Write, "second/")
	second.Paths.Deny = append(second.Paths.Deny, "second/")
	second.DependsOn = append(second.DependsOn, "second")
	second.Breaks = append(second.Breaks, "second")
	second.Postconditions[0].DependsOn = append(second.Postconditions[0].DependsOn, "second")
	second.PostconditionAttempts = append(second.PostconditionAttempts, PostconditionAttempt{PostconditionID: "second"})
	second.Releases = append(second.Releases, Release{Path: "second/z.go"})
	second.Unattributed = append(second.Unattributed, UnattributedWrite{Path: "second/z.go"})

	// Clone resolves each gate's kind and expectation, so the clone's gate carries both.
	s.Equal(Job{
		ID: "a",
		Paths: Paths{
			Write: []string{"types/", "first/"},
			Deny:  []string{"gen/", "first/"},
		},
		DependsOn:             []string{"b", "first"},
		Breaks:                []string{"--old-flag", "first"},
		Postconditions:        []Postcondition{{ID: "unit", Kind: PostconditionKindCheck, Expect: ExpectPassed, Check: checkJSON("test", "."), DependsOn: []string{"check", "first"}}},
		PostconditionAttempts: []PostconditionAttempt{{PostconditionID: "unit", Attempt: CapturedRun{Ref: "out1"}}, {PostconditionID: "first"}},
		Releases:              []Release{{Path: "types/x.go"}, {Path: "first/z.go"}},
		Unattributed:          []UnattributedWrite{{Path: "types/y.go"}, {Path: "first/z.go"}},
	}, first)

	// The original is the store's row and nobody appended through it, so it must still
	// hold exactly what it held.
	s.Equal(Job{
		ID: "a",
		Paths: Paths{
			Write: []string{"types/"},
			Deny:  []string{"gen/"},
		},
		DependsOn:             []string{"b"},
		Breaks:                []string{"--old-flag"},
		Postconditions:        []Postcondition{{ID: "unit", Check: checkJSON("test", "."), DependsOn: []string{"check"}}},
		PostconditionAttempts: []PostconditionAttempt{{PostconditionID: "unit", Attempt: CapturedRun{Ref: "out1"}}},
		Releases:              []Release{{Path: "types/x.go"}},
		Unattributed:          []UnattributedWrite{{Path: "types/y.go"}},
	}, orig)
}

// A run's args sit one level down, in the attempt and in each postcondition's attempt, so they
// alias the store's array unless Clone copies them too. Asserted across two clones for the
// reason TestCopiesEverySliceField gives.
func (s *CloneSuite) TestCopiesAttemptArgs() {
	args := func() []string { return append(make([]string, 0, 4), "-run", "TestOne") }
	orig := Job{
		ID:                    "a",
		Attempt:               &CapturedRun{Ref: "out1", ExtraArgs: args()},
		PostconditionAttempts: []PostconditionAttempt{{PostconditionID: "unit", Attempt: CapturedRun{Ref: "out2", ExtraArgs: args()}}},
	}

	first, second := orig.Clone(), orig.Clone()
	first.Attempt.ExtraArgs = append(first.Attempt.ExtraArgs, "first")
	first.PostconditionAttempts[0].Attempt.ExtraArgs = append(first.PostconditionAttempts[0].Attempt.ExtraArgs, "first")
	second.Attempt.ExtraArgs = append(second.Attempt.ExtraArgs, "second")
	second.PostconditionAttempts[0].Attempt.ExtraArgs = append(second.PostconditionAttempts[0].Attempt.ExtraArgs, "second")
	first.Attempt.ExtraArgs[0] = "-bench"

	s.Equal([]string{"-bench", "TestOne", "first"}, first.Attempt.ExtraArgs)
	s.Equal([]string{"-run", "TestOne", "first"}, first.PostconditionAttempts[0].Attempt.ExtraArgs)
	s.Equal([]string{"-run", "TestOne"}, orig.Attempt.ExtraArgs)
	s.Equal([]string{"-run", "TestOne"}, orig.PostconditionAttempts[0].Attempt.ExtraArgs)
}

// The rejection is the next holder's evidence, so a clone handed out of
// the store must not alias the slices one level down. Asserted across
// two clones for the reason TestCopiesEverySliceField gives.
func (s *CloneSuite) TestCopiesTheRejection() {
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

	s.Equal([]string{"out1", "1"}, second.Rejection.Unmet[0].Violations)
	s.Equal([]string{"out1", "1"}, second.Rejection.Violations)
	s.Equal(Job{ID: "a", Rejection: &Rejection{Unmet: unmet(), Violations: refs()}}, orig)
}

// slices.Clone preserves nil, which is what keeps a row that stored null from coming back
// as [] through the JSON door.
func (s *CloneSuite) TestKeepsNilSlicesNil() {
	c := Job{ID: "a"}.Clone()
	s.Nil(c.Unattributed)
	s.Nil(c.Releases)
	s.Nil(c.Postconditions)
	s.Nil(c.PostconditionAttempts)
}

type PathsSuite struct{ suite.Suite }

func TestPathsSuite(t *testing.T) { suite.Run(t, new(PathsSuite)) }

// intersects asserts the answer both ways round: intersection is symmetric.
func (s *PathsSuite) intersects(a, b string, want bool) {
	s.T().Helper()
	s.Equal(want, PathsIntersect(a, b), "%q and %q", a, b)
	s.Equal(want, PathsIntersect(b, a), "%q and %q, reversed", b, a)
}

func (s *PathsSuite) within(inner, outer string, want bool) {
	s.T().Helper()
	s.Equal(want, PathWithin(inner, outer), "PathWithin(%q, %q)", inner, outer)
}

func (s *PathsSuite) TestIntersectReadsTheClaimGrammar() {
	s.intersects("run.go#A", "run.go#B", false)
	s.intersects("run.go#A", "run.go#A", true)
	s.intersects("run.go#A", "run.go", true)
	s.intersects("run.go#A", ".", true)
	s.intersects("run.go#A", "*.go", true)
	s.intersects("run.go#A", "order.go#A", false)
	s.intersects("pkg/run.go#A", "pkg", true)
	s.intersects("run.go#A", "", false)
	s.Equal("pkg/run.go", LiteralPrefix("pkg/run.go#A"), "a declaration is not a path segment")
}

func (s *PathsSuite) TestIntersectMatchesALiteralAgainstAStarGlobPrecisely() {
	s.intersects("internal/guard/guard.go", "internal/guard/*_test.go", false)
	s.intersects("internal/guard/guard_test.go", "internal/guard/*_test.go", true)
	s.intersects("internal/guard/guard.go#A", "internal/guard/*.go", true)
	s.intersects("internal/guard", "internal/guard/*_test.go", true)
	s.intersects("internal", "internal/guard/*_test.go", true)
	s.intersects("internal/guard/sub/x.go", "internal/guard/*.go", false)
	s.intersects("internal/guard/sub/x.go", "internal/guard/**/*.go", true)
	s.intersects("a/b/c", "a/b/*/d.go", true)
	s.intersects("docs/x.md", "internal/**/*.go", false)
	s.intersects("docs/x.md", "**/*.go", true)
	s.intersects("pkg/run.go", "*.go", false)
	s.intersects("internal/guard/guard.go", "internal/guard/*.go", true)
	s.intersects("internal/guard/*.go", "internal/*/*_test.go", true)
}

func (s *PathsSuite) TestIntersectMatchesALiteralAgainstABraceGlobPrecisely() {
	s.intersects("internal/a/x.go", "internal/{a,b}/x.go", true)
	s.intersects("internal/c/x.go", "internal/{a,b}/x.go", false)
	s.intersects("internal/c/x.go", "internal/{a/b,c}/x.go", true)
}

func (s *PathsSuite) TestWithinReadsLiteralsAndDirectories() {
	s.within("internal/job/a.go", "internal/job/a.go", true)
	s.within("internal/job/a.go", "internal/job", true)
	s.within("internal/job/a.go", "internal/jo", false)
	s.within("internal/job/a.go", "internal/job/*.go", true)
	s.within("internal/job/a.go", "internal/**", true)
	s.within("internal/job/a.txt", "internal/job/*.go", false)
}

func (s *PathsSuite) TestWithinReadsDeclarations() {
	s.within("internal/job/a.go#Run", "internal/job/a.go", true)
	s.within("internal/job/a.go#Run", "internal/job/*.go", true)
	s.within("internal/job/a.go#Run", "internal/job/a.go#Run", true)
	s.within("internal/job/a.go#Run", "internal/job/a.go#Stop", false)
	s.within("internal/job/a.go", "internal/job/a.go#Run", false)
	s.within("internal/job/*.go", "internal/job/a.go#Run", false)
}

func (s *PathsSuite) TestWithinNarrowsGlobsOnly() {
	s.within("internal/job/*.go", "internal/job", true)
	s.within("internal/job/*.go", "internal/job/a.go", false)
	s.within("internal/job/*.go", "internal/**", true)
	s.within("internal/job/*.go", "internal/**/*.go", true)
	s.within("internal/*/x/*.go", "internal/**/*.go", true)
	s.within("internal/job/a.go", "internal/**/*.go", true)
	s.within("internal/*.go", "internal/**/*.go", true)
	s.within("internal/job/**", "internal/**/*.go", false)
	s.within("internal/job/*.txt", "internal/**/*.go", false)
	s.within("internal/job/*_test.go", "internal/**/*.go", true)
	s.within("internal/job/*.go", "internal/**/*_test.go", false)
	s.within("internal/job/*", "internal/**/*.go", false)
	s.within("internal/job/*.{go,txt}", "internal/**/*.go", false)
	s.within("internal/job/*.[a-z]", "internal/**/*.go", false)
	s.within("internal/job/*.go", "internal/**/*", true)
	s.within("internal/**", "internal/job/**", false)
	s.within("**/*.go", "internal/**", false)
	s.within("**/*.go", "**", true)
	s.within("internal/job/*.go", "internal/job/*.go", true)
}

func (s *PathsSuite) TestWithinNeverHoldsTheWholeTreeOrNothingInsideAnything() {
	s.within(".", "internal/**", false)
	s.within(".", "internal/**/*.go", false)
	s.within(".", "internal/job/*.go", false)
	s.within("", "internal/job", false)
	s.within("internal/job/a.go", "", false)
}

type ClaimEvidenceSuite struct{ suite.Suite }

func TestClaimEvidenceSuite(t *testing.T) { suite.Run(t, new(ClaimEvidenceSuite)) }

func (s *ClaimEvidenceSuite) parsesAs(because string, want ClaimEvidence) {
	s.T().Helper()
	got, err := ParseClaimEvidence(because)
	s.Require().NoError(err, because)
	s.Equal(want, got)
	s.Equal(because, got.String(), "the evidence spells back as its because")
}

func (s *ClaimEvidenceSuite) refuses(because, why string) {
	s.T().Helper()
	_, err := ParseClaimEvidence(because)
	s.Require().Error(err, because)
	s.Contains(err.Error(), why)
}

func (s *ClaimEvidenceSuite) TestParseReadsEachKind() {
	s.parsesAs("refs:hint.VCSAdd", ClaimEvidence{Kind: ClaimRefs, Subject: "hint.VCSAdd"})
	s.parsesAs("test:cmd/magus/vcs.go", ClaimEvidence{Kind: ClaimTest, Subject: "cmd/magus/vcs.go"})
	s.parsesAs("output:.:config-generate", ClaimEvidence{Kind: ClaimOutput, Subject: ".:config-generate"})
	s.parsesAs("released", ClaimEvidence{Kind: ClaimReleased})
}

func (s *ClaimEvidenceSuite) TestParseRefusesWhatNamesNoProof() {
	s.refuses("hunch:x", "names no proof")
	s.refuses("refs:", "names no refs subject")
	s.refuses("released:a.go", "released names nothing after it")
	s.refuses("output:generate", "names no target")
}

func (s *ClaimEvidenceSuite) TestParseReadsADeadHolder() {
	e, err := ParseClaimEvidence("dead:api/migrate")
	s.Require().NoError(err)
	s.Equal(ClaimEvidence{Kind: ClaimDeadHolder, Subject: "api/migrate"}, e)
	s.Equal("dead:api/migrate", e.String())

	_, err = ParseClaimEvidence("dead")
	s.EqualError(err, `"dead" names no dead subject`)
}
