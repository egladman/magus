package job

import (
	"bytes"
	"fmt"
	qtypes "github.com/egladman/magus/internal/queue/types"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestPrintLedgerTreeRendersOverlaps(t *testing.T) {
	t.Parallel()

	parent := leaseRow("plan", "")
	parent.Paths.Write = []string{"internal/ledger"}
	parent.Validation = "magus run test internal/ledger"
	child := leaseRow("plan/cli", "plan")
	child.Paths.Write = []string{"internal/ledger/store.go"}

	var out strings.Builder
	PrintTree(&out, job.NewList([]job.Job{parent, child}))
	got := out.String()

	assert.Contains(t, got, "JOB")
	assert.Contains(t, got, "\n  plan/cli", "a child is indented under its parent")
	assert.Contains(t, got, "magus run test internal/ledger")
	assert.Contains(t, got, "overlaps")
	assert.Contains(t, got, "plan and plan/cli claim common ground")
}

func TestPrintJobTreeMarksJobsNobodyIsWaitingOn(t *testing.T) {
	t.Parallel()

	rows := []job.Job{
		{ID: "root", State: job.StatePass},
		{ID: "root/orphan", Parent: "root", State: job.StateRunning, Updated: 100},
		{ID: "late", State: job.StateRunning, Deadline: 50, Updated: 100},
	}
	var out strings.Builder
	PrintTree(&out, job.NewList(rows).Flag(200, time.Minute))
	text := out.String()
	assert.Contains(t, text, "overdue")
	assert.Contains(t, text, "orphan")
	assert.Contains(t, text, "stale")
	assert.Contains(t, text, hint.JobExit.With("root/orphan"))
}

func TestPrintJobTreeCodesTheReadOnlyRows(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	list := job.NewList([]job.Job{{ID: "newer", State: job.StateDeclared}})
	list.ReadOnly = []job.ReadOnly{{Job: "newer", Lacks: []string{"leases-v9"}}}
	PrintTree(&out, list)
	got := out.String()
	assert.Contains(t, got, "["+string(types.StoreRecordsNewer)+"] read-only jobs")
	assert.Contains(t, got, "  newer: requires leases-v9")
}

// HOLDER says who took each job: the checkout it was taken in, the server for its own,
// and nobody for one still waiting to be taken.
func TestPrintJobTreeNamesWhoHoldsEachJob(t *testing.T) {
	t.Parallel()

	rows := []job.Job{
		{ID: "taken", State: job.StateRunning, CheckoutRoot: "/src/ana"},
		{ID: "waiting", State: job.StateDeclared},
		{ID: "sweep", State: job.StateRunning, RegisteredBy: job.Origin{EntryPoint: job.EntryPointServer}},
	}
	var out strings.Builder
	PrintTree(&out, job.NewList(rows))
	holders := map[string]string{}
	for _, line := range strings.Split(out.String(), "\n")[1:4] {
		f := strings.Fields(line)
		holders[f[0]] = f[1]
	}
	assert.Equal(t, map[string]string{"taken": "ana", "waiting": "-", "sweep": "server"}, holders)
}

func TestPrintLedgerTreeSaysWhereAnEmptyPlanComesFrom(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	PrintTree(&out, job.NewList(nil))
	assert.Contains(t, out.String(), fmt.Sprintf("`%s`", hint.JobFork.With("<job>")))
	assert.Contains(t, out.String(), fmt.Sprintf("`%s` MCP tool", hint.ToolClient.String()))
}

// TestPrintJobStatusFailedGateNamesHowToReadIt pins step 3 of the plan that added goals:
// a failed goal named an output ref but not how to read it, leaving the holder to
// reconstruct `magus query output <ref>` by hand. The line must be rendered through
// hint.QueryOutput (never a hardcoded string), and must appear only for a goal that
// actually failed and actually carries a ref.
func TestPrintJobStatusFailedGateNamesHowToReadIt(t *testing.T) {
	t.Parallel()

	status := Status{
		Job:        "plan",
		Violations: []string{`goal "ci": the run behind output ref "outdeadbeef" failed, so its check did not pass`},
		Gates: []job.VerificationStatus{
			{ID: "ci", Verified: false, OutputRef: "outdeadbeef"},
			{ID: "lint", Verified: true, OutputRef: "outfeedface"},
		},
	}

	var out strings.Builder
	PrintStatus(&out, status)
	got := out.String()

	assert.Contains(t, got, "goal ci: rejected (outdeadbeef)")
	assert.Contains(t, got, "magus query output outdeadbeef", "a failed gate must name how to read its ref")
	assert.NotContains(t, got, "magus query output outfeedface", "a verified gate needs no query-output line")
}

func TestPrintJobStatusRendersTheFootprint(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status Status
		want   string
	}{
		{
			name: "known",
			status: Status{Job: "plan", Verified: true, FootprintKnown: true, Footprint: []job.RegionChange{
				{File: job.FileChange{Path: "a.go"}, Side: job.RegionNew, Lines: [2]int{3, 9}, Declaration: "func X() {", Driver: "golang"},
				{File: job.FileChange{Path: "a.go"}, Side: job.RegionNew, Lines: [2]int{12, 14}, Declaration: "func X() {", Driver: "golang"},
				{File: job.FileChange{Path: "a.go"}, Side: job.RegionOld, Lines: [2]int{20, 21}, Declaration: "func Y() {", Driver: "golang"},
				{File: job.FileChange{Path: "notes.txt"}, Side: job.RegionNew, Lines: [2]int{1, 2}},
				{File: job.FileChange{Path: "notes.txt"}, Side: job.RegionNew, Lines: [2]int{8, 8}},
			}},
			want: "verified plan, recorded pass\n" +
				"footprint: where its diff since the checkpoint landed\n" +
				"  a.go#func X() {\n" +
				"  a.go#func Y() {\n" +
				"  notes.txt:1-2\n" +
				"  notes.txt:8-8\n",
		},
		{
			name:   "known and empty",
			status: Status{Job: "plan", Verified: true, FootprintKnown: true},
			want: "verified plan, recorded pass\n" +
				"footprint: no line changed since the checkpoint\n",
		},
		{
			name:   "declined",
			status: Status{Job: "plan", Verified: true, FootprintReason: "git does not report changed regions (RegionReporter)"},
			want: "verified plan, recorded pass\n" +
				"footprint: not known, git does not report changed regions (RegionReporter)\n",
		},
		{
			name:   "nobody looked",
			status: Status{Job: "plan", Verified: true},
			want: "verified plan, recorded pass\n" +
				"footprint: not known, nothing observed the tree\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out strings.Builder
			PrintStatus(&out, tc.status)
			assert.Equal(t, tc.want, out.String())
		})
	}
}

func TestPrintJobTreeRendersTheClaimsVerdict(t *testing.T) {
	t.Parallel()

	rows := []job.Job{
		{ID: "a", State: job.StateRunning, Paths: job.Paths{Write: []string{"run.go#executeStages"}}},
		{ID: "b", State: job.StateRunning, Paths: job.Paths{Write: []string{"run.go#RunCI"}}},
	}
	var out strings.Builder
	PrintTree(&out, job.NewList(rows))
	_, after, ok := strings.Cut(out.String(), "\noverlaps\n")
	require.True(t, ok, "two claims on one file are listed: %s", out.String())
	assert.Equal(t, "  a and b claim common ground\n"+
		"    a: run.go#executeStages\n"+
		"    b: run.go#RunCI\n"+
		"    claims: disjoint (different declarations of one file: an integration order, not a wait)\n", after)
}

func TestPrintJobTreeRendersEachFootprintVerdict(t *testing.T) {
	t.Parallel()

	pair := func(f *job.OverlapFootprint) job.Overlap {
		return job.Overlap{JobA: "a", JobB: "b", PathsA: []string{"api"}, PathsB: []string{"api/x.go"}, Footprint: f}
	}
	rows := []job.Job{
		{ID: "a", State: job.StateRunning, Paths: job.Paths{Write: []string{"api"}}},
		{ID: "b", State: job.StateRunning, Paths: job.Paths{Write: []string{"api/x.go"}}},
	}
	for _, tc := range []struct {
		name string
		f    *job.OverlapFootprint
		want string
	}{
		{"disjoint", &job.OverlapFootprint{Verdict: job.FootprintDisjoint}, "    footprints: disjoint\n"},
		{"shared", &job.OverlapFootprint{Verdict: job.FootprintShared, Shared: []string{"a.go#func X", "b.go#func Y"}},
			"    footprints: shared (a.go#func X, b.go#func Y)\n"},
		{"unknown", &job.OverlapFootprint{Verdict: job.FootprintUnknown, Reason: "b: no checkout of this repository is bound to b"},
			"    footprints: unknown (b: no checkout of this repository is bound to b)\n"},
		{"not computed", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out strings.Builder
			PrintTree(&out, job.List{Jobs: rows, Overlaps: []job.Overlap{pair(tc.f)}})
			_, after, ok := strings.Cut(out.String(), "    b: api/x.go\n")
			require.True(t, ok, "the pair's paths are rendered: %s", out.String())
			assert.Equal(t, tc.want, after)
		})
	}
}

func TestForkOutputCarriesReferencedOutsideBesideTheRow(t *testing.T) {
	t.Parallel()

	out := ForkOutput{
		Job:               job.Job{ID: "fork-names-refs-outside"},
		ReferencedOutside: []ReferencedFile{{File: "types/conformance_test.go", Count: 9, Uses: []string{"types/conformance.go"}}},
	}
	raw, err := codec.Marshal(ForkReport{ForkOutput: out})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, codec.Unmarshal(raw, &got))
	assert.Equal(t, "fork-names-refs-outside", got["id"], "the row flattens")
	assert.Equal(t, []any{map[string]any{"file": "types/conformance_test.go", "count": float64(9), "uses": []any{"types/conformance.go"}}},
		got["referenced_outside"])
}

// inflightFixture is the in-flight plan's fixture: two job trees, one checkout on
// fork-row-rebase, eight open changes on main listed out of plan order, one plan of one
// partition and one verdict.
func inflightFixture() (job.List, InflightInput) {
	rows := []job.Job{
		{ID: "fleet", State: job.StateRunning},
		{ID: "child-job-wait", Parent: "fleet", State: job.StatePass, CheckoutRoot: "/w/child-job-wait"},
		{ID: "status-wait", Parent: "fleet", State: job.StateRunning, CheckoutRoot: "/w/status-wait"},
		{ID: "fork-row-rebase", Parent: "fleet", State: job.StateRunning, CheckoutRoot: "/w/fork-row-rebase"},
		{ID: "inflight-view", Parent: "fleet", State: job.StateRunning, ReadOnly: true, CheckoutRoot: "/w/main"},
		{ID: "their-fleet", State: job.StateRunning},
		{ID: "their-work", Parent: "their-fleet", State: job.StateRunning, CheckoutRoot: "/w/theirs"},
	}
	change := func(id, branch, title, author string) qtypes.Change {
		return qtypes.Change{ID: id, Head: strings.Repeat(id, 14)[:40], Branch: branch, Base: "main", Title: title, Author: author, Method: qtypes.MethodSquash}
	}
	c438 := change("438", "guard-reads-and-searches", "guard reads and searches", "egladman")
	c443 := change("443", "fork-row-rebase", "rebase the fork row onto the job store before a worker forks its own child", "egladman")
	c445 := change("445", "trimpath-tests-more", "trimpath the remaining tests", "egladman")
	c450 := change("450", "merge-queue-dashboard", "merge queue dashboard", "egladman")
	c456 := change("456", "fix-444-review", "fix the 444 review", "egladman")
	c457 := change("457", "theirs", "their change", "priya")
	c459 := change("459", "status-wait", "status wait pipe", "egladman")
	in := InflightInput{
		Fetch: &InflightFetch{Provider: "github", Host: "github.com", Base: "main", At: 1790514000, ElapsedMS: 812},
		Changes: qtypes.Changes{
			Base:    "main",
			Changes: []qtypes.Change{c443, c438, c450, c445, c456, c457, c459},
			Unqueued: []qtypes.UnqueuedChange{
				{ID: "458", Head: strings.Repeat("8", 40), Branch: "child-job-wait", Base: "main", Title: "child job wait", Author: "egladman", Mark: qtypes.MarkKickedBack},
			},
		},
		Plan: &qtypes.Plan{
			Base: "main", BaseCommit: "f223bb1" + strings.Repeat("0", 33), Depth: 1,
			Partitions: [][]qtypes.Change{{c438, c443, c445, c450, c456, c457}},
			Verdicts:   []qtypes.Verdict{{Change: c459, Decision: qtypes.DecisionKick, Code: qtypes.CodeKickRed, Reason: "pr hygiene: changelog fragment missing"}},
		},
		Branches: map[string]string{
			"/w/child-job-wait": "child-job-wait", "/w/status-wait": "status-wait", "/w/fork-row-rebase": "fork-row-rebase",
			"/w/main": "main", "/w/theirs": "theirs",
		},
		Me: Identity{Lease: "fork-row-rebase", Login: "egladman"},
	}
	return job.List{Jobs: rows}, in
}

// The plan's proof: the reader's two kicked-back changes first, the five queued in plan
// order whatever order the provider listed them in, and everyone else as one count line.
func TestLsJobsPrintsTheChangesInFlight(t *testing.T) {
	t.Parallel()
	list, in := inflightFixture()
	render := func(all bool) string {
		var out strings.Builder
		PrintInflight(&out, Inflight(list, in), all)
		return out.String()
	}
	head := "in flight on main at f223bb100000, as of 2026-09-27 13:00 UTC (github, github.com, 812ms)\n"
	kicked := "" +
		"  #459  status wait pipe  KICK_RED: pr hygiene: changelog fragment missing  job status-wait\n" +
		"  #458  child job wait    kicked back                                       job child-job-wait\n"
	queued := "" +
		"  #438  guard reads and searches                            pos 1\n" +
		"  #443  rebase the fork row onto the job store before a...  pos 2  job fork-row-rebase\n" +
		"  #445  trimpath the remaining tests                        pos 3\n" +
		"  #450  merge queue dashboard                               pos 4\n" +
		"  #456  fix the 444 review                                  pos 5\n"
	tail := "jobs without a change (1)\n  inflight-view\n"
	refetch := "refetch: magus queue ls --provider github --base main\n"

	assert.Equal(t, fmt.Sprintf(`%sneeds me (2)
%squeued, in plan order (5)
%s%sothers: 1 queued
%s`, head, kicked, queued, tail, refetch), render(false))
	assert.Equal(t, fmt.Sprintf(`%sneeds the author (2)
%squeued, in plan order (6)
%s  #457  their change                                        pos 6  job their-work
%s%s`, head, kicked, queued, tail, refetch), render(true))
	assert.Equal(t, render(false), render(false), "one store and one snapshot render the same bytes")
}

func TestLsJobsRendersTheRecordTheSameTwice(t *testing.T) {
	t.Parallel()
	list, in := inflightFixture()
	first, err := codec.Marshal(Inflight(list, in))
	require.NoError(t, err)
	second, err := codec.Marshal(Inflight(list, in))
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second))
}

func TestDescribeJobPrintsItsChange(t *testing.T) {
	t.Parallel()
	list, in := inflightFixture()
	joined := Inflight(list, in)
	var out strings.Builder
	PrintChange(&out, joined.Changes, "fork-row-rebase")
	PrintChange(&out, joined.Changes, "child-job-wait")
	PrintChange(&out, joined.Changes, "inflight-view")
	assert.Equal(t, "\nchange: #443 rebase the fork row onto the job store before a..., the queue's turn, pos 2\n"+
		"\nchange: #458 child job wait, its author's turn, kicked back\n", out.String())
}

func TestLsJobsClipsATitleAtAWord(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "short", clipTitle("short", 48))
	assert.Equal(t, "one two...", clipTitle("one two three", 9))
	assert.Equal(t, "abcdefgh...", clipTitle("abcdefghijk", 8), "no space to cut at")
}

// The MODEL column names the model the row asks for and falls back to the tier the store
// assigned, so a row that names nothing still shows what it runs at.
func TestJobModelCellFallsBackToTheAssignedTier(t *testing.T) {
	t.Parallel()

	tier := &tier.Assignment{Name: "routine"}
	tests := []struct {
		name string
		row  job.Job
		want string
	}{
		{"the model the row names wins", job.Job{Agent: &job.Agent{Model: job.Model{ID: "opus"}, Worker: job.Worker{Type: job.WorkerTypeResumed}}, ModelTier: tier}, "opus"},
		{"the assigned tier stands in", job.Job{ModelTier: tier}, "routine"},
		{"neither is a dash", job.Job{}, "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, modelCell(tt.row))
		})
	}
}

// The fork says which magus its checkout got, and that an adopted checkout is never removed.
func TestPrintJobWorktreeSaysWhichMagusTheCheckoutGot(t *testing.T) {
	made := job.Worktree{Root: "/w/c", Base: "0123456789abcdef", Made: true, Binary: "/w/c/magus"}
	for name, tc := range map[string]struct {
		wt    job.Worktree
		built bool
		want  string
	}{
		"built from its own sources": {wt: made, built: true, want: "built /w/c/magus from the checkout's own sources"},
		"a copy":                     {wt: made, want: "placed /w/c/magus, the magus the forking checkout runs"},
		"a forker on PATH":           {wt: job.Worktree{Root: "/w/c", Made: true}, want: "runs magus from PATH"},
		"adopted":                    {wt: job.Worktree{Root: "/w/c", Base: "0123456789abcdef"}, want: "adopted for job w at 0123456789ab; its holder takes it there with `"},
	} {
		var out bytes.Buffer
		PrintWorktree(&out, job.Job{ID: "w", Worktree: &tc.wt}, tc.built)
		assert.Contains(t, out.String(), tc.want, name)
	}
}

type DescribeJobRecordSuite struct {
	suite.Suite
	out DescribeOutput
}

func TestDescribeJobRecordSuite(t *testing.T) { suite.Run(t, new(DescribeJobRecordSuite)) }

func (s *DescribeJobRecordSuite) SetupTest() {
	s.out = DescribeOutput{Terms: Terms{Lease: job.Job{
		ID:    "wave/a",
		Paths: job.Paths{Write: []string{"a.go"}},
		Check: &job.LeaseCheck{Target: "test", Project: "."},
		State: job.StateDeclared,
	}}}
}

func (s *DescribeJobRecordSuite) printed() map[string]codec.RawMessage {
	raw, err := codec.Marshal(s.out)
	s.Require().NoError(err)
	var members map[string]codec.RawMessage
	s.Require().NoError(codec.Unmarshal(raw, &members))
	return members
}

func (s *DescribeJobRecordSuite) TestTheJobNamesTheAPIVersionApplyReads() {
	var row map[string]codec.RawMessage
	s.Require().NoError(codec.Unmarshal(s.printed()["job"], &row))
	s.JSONEq(fmt.Sprintf("%q", job.APIVersion), string(row["api_version"]))
}

func (s *DescribeJobRecordSuite) TestTheTermsAndGoalsStayBesideTheJob() {
	members := s.printed()
	s.Contains(members, "goals")
	s.Contains(members, "stage")
	s.Contains(members, "job")
}

func (s *DescribeJobRecordSuite) TestTheWholeRecordRoundTripsThroughApply() {
	raw, err := codec.Marshal(s.out)
	s.Require().NoError(err)
	rows, err := DecodeDeclarations(bytes.NewReader(raw))
	s.Require().NoError(err)
	s.Require().Len(rows, 1)
	s.Equal("wave/a", rows[0].ID)
}

func (s *DescribeJobRecordSuite) TestTheJobMemberAloneRoundTripsThroughApply() {
	rows, err := DecodeDeclarations(bytes.NewReader(s.printed()["job"]))
	s.Require().NoError(err)
	s.Require().Len(rows, 1)
	s.Equal([]string{"a.go"}, rows[0].Paths.Write)
}

// applyStateSuite pins what `job apply` says of a state it moved.
type applyStateSuite struct {
	suite.Suite
}

func TestApplyState(t *testing.T) { suite.Run(t, new(applyStateSuite)) }

func (s *applyStateSuite) printed(from, to job.State, dryRun bool) string {
	var out bytes.Buffer
	PrintApply(&out, []Applied{{
		Prev:    job.Job{ID: "w", State: from, Paths: job.Paths{Write: []string{"a.go"}}},
		Next:    job.Job{ID: "w", State: to, Paths: job.Paths{Write: []string{"a.go", "b.go"}}},
		Changed: []string{"paths.write"},
	}}, nil, dryRun)
	return out.String()
}

func (s *applyStateSuite) TestAKeptStateReadsStill() {
	s.Contains(s.printed(job.StateRunning, job.StateRunning, false), "updated w, still running:\n  paths.write +b.go\n")
}

func (s *applyStateSuite) TestAMovedStateNamesBoth() {
	s.Contains(s.printed(job.StateExited, job.StateDeclared, false), "updated w, exited to declared, so its holder takes it with `magus job exec w`:\n  paths.write +b.go\n")
}

func (s *applyStateSuite) TestADryRunNamesTheMoveItWouldMake() {
	s.Contains(s.printed(job.StateExited, job.StateDeclared, true), "would update w, exited to declared, so its holder takes it with `magus job exec w`:\n")
}

// A queue never fetched says so, and the reader is told which command reads it.
func TestPrintInflightSaysWhenTheQueueWasNeverFetched(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	PrintInflight(&out, InflightList{}, false)
	assert.Contains(t, out.String(), "in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes\n")
}

// Ended rows pile up, so the board lists the live ones and counts the rest.
func TestWithoutEndedDropsEveryRowThatEndedAndItsReadOnlyShadows(t *testing.T) {
	t.Parallel()
	list := job.List{
		Jobs: []job.Job{
			{ID: "fleet", State: job.StateRunning},
			{ID: "done", State: job.StatePass},
			{ID: "lost", State: job.StateNoReturn},
			{ID: "handed-in", State: job.StateExited},
		},
		ReadOnly: []job.ReadOnly{{Job: "done"}, {Job: "fleet"}},
	}
	live, ended := WithoutEnded(list)
	var ids []string
	for _, row := range live.Jobs {
		ids = append(ids, row.ID)
	}
	assert.Equal(t, []string{"fleet", "handed-in"}, ids, "an exited row still holds its lease, so it is live")
	assert.Equal(t, 2, ended)
	require.Len(t, live.ReadOnly, 1)
	assert.Equal(t, "fleet", live.ReadOnly[0].Job)
}

func leaseRow(id, parent string) job.Job {
	return job.Job{ID: id, Parent: parent, State: job.StateRunning, Agent: &job.Agent{Model: job.Model{Tier: "bounded"}, Worker: job.Worker{Type: job.WorkerTypeResumed}}}
}
