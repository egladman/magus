package ci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/safetyscissors/budget"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/check"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// TestDecideGateMatrix pins every cell of the decision matrix by hand. The
// expectations are written out rather than derived, so a change to DecideGate's
// logic fails here instead of being restated by the test.
func TestDecideGateMatrix(t *testing.T) {
	cases := []struct {
		name string
		in   gateFacts
		want gateDecision
	}{
		{"nothing on record", gateFacts{}, gateRun},
		{"not redundant, forced", gateFacts{Forced: true}, gateRun},
		{"not redundant, nested", gateFacts{Nested: true}, gateRun},
		{"not redundant, forced and nested", gateFacts{Forced: true, Nested: true}, gateRun},

		// Redundant refuses on its own; no machine-load condition gates it.
		{"redundant", gateFacts{Redundant: true}, gateRefuse},
		{"redundant, forced", gateFacts{Redundant: true, Forced: true}, gateRun},
		// Nested is the one caller that cannot refuse: it counts its own ancestors'
		// claims as load.
		{"redundant, nested", gateFacts{Redundant: true, Nested: true}, gateAdvise},
		{"redundant, forced and nested", gateFacts{Redundant: true, Forced: true, Nested: true}, gateRun},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, decideGate(tc.in))
		})
	}
}

func TestPoolSaturated(t *testing.T) {
	cases := []struct {
		name string
		snap *budget.Snapshot
		want bool
	}{
		{"nil snapshot fails open", nil, false},
		{"idle", &budget.Snapshot{BudgetSlots: 8, HeldSlots: 2}, false},
		{"all slots held", &budget.Snapshot{BudgetSlots: 8, HeldSlots: 8}, true},
		{"all memory held", &budget.Snapshot{BudgetMB: 1000, HeldMB: 1000}, true},
		{"unlimited axes never saturate", &budget.Snapshot{HeldSlots: 100, HeldMB: 100000}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, poolSaturated(tc.snap))
		})
	}
}

func TestGateFingerprint(t *testing.T) {
	a := []gateStep{{Project: ".", Target: "ci", Key: "k1"}, {Project: "docs", Target: "ci", Key: "k2"}}
	b := []gateStep{{Project: "docs", Target: "ci", Key: "k2"}, {Project: ".", Target: "ci", Key: "k1"}}
	assert.Equal(t, gateFingerprint(a), gateFingerprint(b), "order-insensitive")
	assert.NotEqual(t, gateFingerprint(a), gateFingerprint([]gateStep{{Project: ".", Target: "ci", Key: "k1"}}), "selection is part of the identity")
	assert.NotEqual(t, gateFingerprint(a), gateFingerprint([]gateStep{{Project: ".", Target: "ci", Key: "OTHER"}, {Project: "docs", Target: "ci", Key: "k2"}}), "a changed key changes the fingerprint")
	assert.Empty(t, gateFingerprint(nil), "an empty selection has no identity")
}

func TestMergeFreeRange(t *testing.T) {
	linear := []types.Commit{
		{ID: "c3", Parents: []string{"c2"}},
		{ID: "c2", Parents: []string{"c1"}},
		{ID: "c1", Parents: []string{"c0"}},
	}
	assert.True(t, MergeFreeRange(linear, "c1"), "linear history down to green")
	assert.True(t, MergeFreeRange(linear, "c3"), "green at head is a trivially clean range")
	assert.False(t, MergeFreeRange(linear, "c0"), "green outside the walked window cannot be vouched for")

	// A merge always re-gates, however clean: it combines two verified
	// histories into a tree neither gate saw.
	merged := []types.Commit{
		{ID: "m1", Parents: []string{"c2", "f1"}},
		{ID: "c2", Parents: []string{"c1"}},
		{ID: "c1", Parents: []string{"c0"}},
	}
	assert.False(t, MergeFreeRange(merged, "c1"), "merge between green and head")
	assert.True(t, MergeFreeRange(merged, "m1"), "green AT the merge is behind us, not in the range")
}

// TestPRMergeFreeRange: under a pull-request checkout the head IS a merge the
// provider synthesized, so it is exempt; a merge anyone pushed still refuses.
func TestPRMergeFreeRange(t *testing.T) {
	synthetic := []types.Commit{
		{ID: "pr-merge", Parents: []string{"main1", "c2"}},
		{ID: "c2", Parents: []string{"c1"}},
		{ID: "c1", Parents: []string{"c0"}},
	}
	assert.True(t, PRMergeFreeRange(synthetic, "c1"), "the synthetic head merge is the harness's, not the delta's")
	assert.False(t, MergeFreeRange(synthetic, "c1"), "the plain rule still refuses it")

	pushed := []types.Commit{
		{ID: "pr-merge", Parents: []string{"main1", "m1"}},
		{ID: "m1", Parents: []string{"c2", "f1"}},
		{ID: "c2", Parents: []string{"c1"}},
	}
	assert.False(t, PRMergeFreeRange(pushed, "c1"), "a merge below the synthetic head is in the range")
	assert.True(t, PRMergeFreeRange(synthetic, "pr-merge"), "green at the head is a trivially clean range")
	assert.False(t, PRMergeFreeRange(nil, "c1"), "no history cannot be vouched for")
}

func TestInheritOff(t *testing.T) {
	assert.False(t, inheritOff(nil), "on is the default")
	assert.False(t, inheritOff([]*types.Project{{Path: "."}, {Path: "docs"}}))
	assert.True(t, inheritOff([]*types.Project{{Path: "."}, {Path: "docs", GateInheritOff: true}}),
		"one declaration turns it off workspace-wide")
}

// A dropped option is indistinguishable from a dropped OPT-OUT, and the opt-outs here
// all fail open: an ignored `gate_inherit = false` reads as inheritance on, which skips
// the very CI run the author demanded. So a magusfile this binary could not fully read
// does not get to skip CI.
func TestInheritOffWhenAnOptionWasDropped(t *testing.T) {
	assert.True(t, inheritOff([]*types.Project{
		{Path: "."},
		{Path: "docs", IgnoredOptions: []string{"gate_something_new"}},
	}), "a binary that did not understand the whole magusfile must not inherit a verdict")
}

// newInheritProbe builds a probe whose provider, history and assessment are all stubbed,
// so the decision is exercised with no repository and no CI provider.
func newInheritProbe(green string, found bool, history []types.Commit, rep types.TierReport) inheritProbe {
	return inheritProbe{
		LastGreenRun: func(context.Context) (string, string, bool) {
			return "https://example/run/7", green, found
		},
		History: func(context.Context) ([]types.Commit, error) { return history, nil },
		Assess:  func(context.Context, string) (types.TierReport, error) { return rep, nil },
	}
}

var (
	proseReport = types.TierReport{Tier: check.RiskTrivial, Evidence: []check.TierEvidence{
		{Path: "docs/x.md", Class: "prose", Tier: check.RiskTrivial, Why: `matches "**/*.md" (built-in default); nothing in ci's chain reads it`},
	}}
	codeReport = types.TierReport{Tier: check.RiskScoped, Evidence: []check.TierEvidence{
		{Path: "docs/x.md", Class: "prose", Tier: check.RiskTrivial, Why: "prose"},
		{Path: "internal/y.go", Class: "code", Tier: check.RiskScoped, Why: "Go package y"},
	}}
	// embedReport is the regression the tier exists for: markdown a package compiles in
	// is prose by class, and inheriting over it skipped the tests that read it.
	embedReport = types.TierReport{Tier: check.RiskScoped, Evidence: []check.TierEvidence{
		{Path: "internal/agent/skills/run/SKILL.md", Class: "prose", Tier: check.RiskScoped, Why: "embedded by Go package skills (go:embed)"},
	}}
)

var inheritHistory = []types.Commit{
	{ID: "pr-merge", Parents: []string{"main1", "c2"}},
	{ID: "c2", Parents: []string{"green0123456789"}},
	{ID: "green0123456789", Parents: []string{"c0"}},
}

// TestInheritProbeFires pins the whole hit: the finding, and both reports it
// publishes. The report text is spelled out rather than derived, because an
// inherited verdict is only defensible if a reader can reconstruct it; a
// change that thins the report has to fail here.
func TestInheritProbeFires(t *testing.T) {
	var assessedAt string
	p := newInheritProbe("green0123456789", true, inheritHistory, proseReport)
	p.Assess = func(_ context.Context, green string) (types.TierReport, error) {
		assessedAt = green
		return proseReport, nil
	}
	got, ok := p.Evaluate(context.Background())
	require.True(t, ok)
	assert.Equal(t, InheritFinding{Run: "https://example/run/7", Commit: "green0123456789", Report: proseReport}, got)
	assert.Equal(t, "green0123456789", assessedAt, "the change is measured from the green commit")

	assert.Equal(t, "verdict inherited from run https://example/run/7 (commit green012): "+
		"the change since that green run tiers trivial\n"+
		`docs/x.md: trivial (prose: matches "**/*.md" (built-in default); nothing in ci's chain reads it)`, got.AnnotationText())

	assert.Equal(t, "### Inherited verdict\n\n"+
		"The shard fan-out was short-circuited: the change since this branch's "+
		"last green CI run tiers trivial, so that run's verdict stands.\n\n"+
		"Inherited run: https://example/run/7 at commit `green012`.\n\n"+
		"| Changed path | Tier | Class | Decided by |\n| --- | --- | --- | --- |\n"+
		"| `docs/x.md` | trivial | prose | matches \"**/*.md\" (built-in default); nothing in ci's chain reads it |\n\n"+
		"To dispute a row, its last column names the declaration or mechanism that "+
		"decided it; to turn inheritance off, declare `gate_inherit = false` in "+
		"magus.project.\n", got.SummaryMarkdown())
}

// TestInheritProbeDeclines: every refusal reports ok=false and an empty
// finding, so the plan emits nothing at all from this feature.
func TestInheritProbeDeclines(t *testing.T) {
	pushedMerge := []types.Commit{
		{ID: "pr-merge", Parents: []string{"main1", "m1"}},
		{ID: "m1", Parents: []string{"c2", "f1"}},
		{ID: "c2", Parents: []string{"green0123456789"}},
		{ID: "green0123456789", Parents: []string{"c0"}},
	}
	cases := map[string]inheritProbe{
		"no green run":          newInheritProbe("green0123456789", false, inheritHistory, proseReport),
		"green run unnamed":     newInheritProbe("", true, inheritHistory, proseReport),
		"code in the delta":     newInheritProbe("green0123456789", true, inheritHistory, codeReport),
		"embedded markdown":     newInheritProbe("green0123456789", true, inheritHistory, embedReport),
		"a comment-only change": newInheritProbe("green0123456789", true, inheritHistory, types.TierReport{Tier: check.RiskMechanical}),
		"merge in the range":    newInheritProbe("green0123456789", true, pushedMerge, proseReport),
		"green out of reach":    newInheritProbe("unreachable", true, inheritHistory, proseReport),
		"disabled":              {Disabled: true},
		"no provider wired":     {},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := p.Evaluate(context.Background())
			assert.False(t, ok)
			assert.Equal(t, InheritFinding{}, got, "a declined probe carries nothing")
		})
	}
}

// A failing history or diff call declines rather than propagating: the plan
// proceeds exactly as it would have without this feature.
func TestInheritProbeSurvivesBrokenInputs(t *testing.T) {
	boom := errors.New("no such revision")
	p := newInheritProbe("green0123456789", true, inheritHistory, proseReport)
	p.History = func(context.Context) ([]types.Commit, error) { return nil, boom }
	_, ok := p.Evaluate(context.Background())
	assert.False(t, ok, "an unreadable history declines")

	p = newInheritProbe("green0123456789", true, inheritHistory, proseReport)
	p.Assess = func(context.Context, string) (types.TierReport, error) { return proseReport, boom }
	_, ok = p.Evaluate(context.Background())
	assert.False(t, ok, "an unreadable diff declines")
}

// An empty delta is the boundary case: nothing changed since green, so the
// verdict inherits and both reports say so rather than printing an empty table.
func TestInheritProbeEmptyDelta(t *testing.T) {
	p := newInheritProbe("green0123456789", true, inheritHistory, types.TierReport{Tier: check.RiskTrivial})
	got, ok := p.Evaluate(context.Background())
	require.True(t, ok)
	assert.Contains(t, got.AnnotationText(), "\nno paths changed since that run")
	assert.Contains(t, got.SummaryMarkdown(), "No paths changed since that run.\n")
	assert.NotContains(t, got.SummaryMarkdown(), "| Changed path |")
}

// GateScopeSuite pins running-doc entry 277 at the writer: a gate record names whether it
// covered every project the branch affects, so `magus run ci libs/cmdline` never records
// itself as the branch gate.
type GateScopeSuite struct {
	suite.Suite
}

func TestGateScopeSuite(t *testing.T) { suite.Run(t, new(GateScopeSuite)) }

func (s *GateScopeSuite) affected(paths ...string) []types.Target {
	out := make([]types.Target, len(paths))
	for i, p := range paths {
		out[i] = types.Target{Path: p, Name: types.TargetCI}
	}
	return out
}

func (s *GateScopeSuite) TestOneProjectOfSeveralAffectedIsProjectScope() {
	s.Equal(sessions.GateScopeProject, scopeOver(s.affected(".", "docs", "libs/cmdline"), []string{"libs/cmdline"}))
}

func (s *GateScopeSuite) TestEveryAffectedProjectIsAffectedScope() {
	s.Equal(sessions.GateScopeAffected, scopeOver(s.affected("docs", "libs/cmdline"), []string{".", "docs", "libs/cmdline"}),
		"`magus run ci` over a superset of the affected set covers the branch")
}

func (s *GateScopeSuite) TestNothingAffectedIsAffectedScope() {
	s.Equal(sessions.GateScopeAffected, scopeOver(nil, []string{"libs/cmdline"}))
}

func (s *GateScopeSuite) TestAProjectScopedPassIsNotTheBranchGate() {
	testkit.Isolate(s.T())
	root := s.T().TempDir()
	g := &GateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1",
		projects: []string{"libs/cmdline"}, scope: sessions.GateScopeProject}
	g.Record(context.Background(), nil, false, nil)

	dir, err := sessions.Dir(root)
	s.Require().NoError(err)
	_, ok := sessions.LatestGate(fold2(s.T(), dir), "b", types.TargetCI)
	s.False(ok, "the redundancy check and the guard both read the branch gate through LatestGate")

	rec, ok := sessions.GateAt(fold2(s.T(), dir), "c1", types.TargetCI)
	s.Require().True(ok)
	s.Equal(sessions.GateScopeProject, rec.Scope, "the scope rides the record")
}

// seedGreenGate records a passing ci gate for branch b with fingerprint fp in
// the session store XDG_STATE_HOME points at.
func seedGreenGate(t *testing.T, root, fp string) {
	t.Helper()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	require.NoError(t, sessions.RecordGate(dir, sessions.GateResult{
		Target: types.TargetCI, Scope: sessions.GateScopeAffected, Ref: "b", Commit: "c1",
		Outcome: sessions.OutcomePass, Fingerprint: fp,
		Projects: []string{"."}, Charms: []string{"quiet"},
		Inv: "inv123",
	}, sessions.InvocationStart{Workspace: root}))
}

// poolProbe answers a fixed load, so no test needs a broker.
func poolProbe(saturated bool) func(context.Context) (bool, string) {
	state := "idle: 2 of 8 slots held"
	if saturated {
		state = "saturated: 8 of 8 slots held, 2 runs queued"
	}
	return func(context.Context) (bool, string) { return saturated, state }
}

// TestGateEvaluateRefusesUnderLoad is the refusal: a recorded green gate with
// identical inputs plus a saturated pool exits 75 with MGS3010. The message
// must let a reader reconstruct the decision: the green gate's run ref, branch
// and timestamp, the finding, the pool state, the override, the alternative.
func TestGateEvaluateRefusesUnderLoad(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	seedGreenGate(t, root, "fp-1")

	g := &GateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1", scope: sessions.GateScopeAffected}
	g.poolProbe = poolProbe(true)
	err := g.Evaluate(context.Background(), false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.RedundantGateDeferred), "the refusal carries MGS3010")
	assert.Equal(t, exit.TempFail, exit.Code(err), "the refusal states EX_TEMPFAIL, the same convention as MGS3009")

	msg := err.Error()
	assert.Contains(t, msg, "run inv123", "names the green gate's run ref")
	assert.Contains(t, msg, "branch b", "names the branch")
	assert.Contains(t, msg, "commit c1", "names the green gate's commit")
	assert.Contains(t, msg, "recorded 20", "carries the RFC3339 timestamp")
	assert.Contains(t, msg, "matches it exactly", "names the identical-fingerprint finding")
	assert.Contains(t, msg, "machine pool: saturated: 8 of 8 slots held, 2 runs queued", "prints the pool state it saw")
	assert.Contains(t, msg, "--no-redundancy-check", "names the override")
	assert.Contains(t, msg, "codes/sandbox/MGS3010/", "links the code page, which names pushing as the alternative")
}

// TestGateRefusalRecordsDeferral: the refusal persists as a deferred gate
// record pointing at the green gate, and that record never shadows the green
// verdict: the next evaluation still finds it and still refuses.
func TestGateRefusalRecordsDeferral(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	seedGreenGate(t, root, "fp-1")

	g := &GateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c2", fp: "fp-1", projects: []string{"."}, scope: sessions.GateScopeAffected}
	g.poolProbe = poolProbe(true)
	require.Error(t, g.Evaluate(context.Background(), false))

	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	rec, ok := sessions.LatestGate(fold, "b", types.TargetCI)
	require.True(t, ok)
	// The deferral does not shadow the green verdict.
	assert.Equal(t, sessions.GateRecord{
		GateResult: sessions.GateResult{
			Target: types.TargetCI, Scope: sessions.GateScopeAffected, Ref: "b", Commit: "c1",
			Outcome: sessions.OutcomePass, Fingerprint: "fp-1",
			Projects: []string{"."}, Charms: []string{"quiet"},
			Inv: "inv123",
		},
		At: rec.At,
	}, rec)

	// The deferral itself is on record, interrogable after the fact.
	var deferred *sessions.GateResult
	for _, r := range fold.Records {
		if r.Kind != sessions.KindGateResult {
			continue
		}
		var payload sessions.GateResult
		require.NoError(t, json.Unmarshal(r.Payload, &payload))
		if payload.Outcome == sessions.OutcomeDeferred {
			deferred = &payload
		}
	}
	require.NotNil(t, deferred, "the refusal persisted a deferred gate record")
	// It points at the green gate it deferred to, from the commit the refused run was at.
	assert.Equal(t, sessions.GateResult{
		Target: types.TargetCI, Scope: sessions.GateScopeAffected, Ref: "b", Commit: "c2",
		Outcome: sessions.OutcomeDeferred, Fingerprint: "fp-1",
		Projects:   []string{"."},
		DeferredTo: "c1",
	}, *deferred)

	require.Error(t, g.Evaluate(context.Background(), false), "still refuses after its own deferral record")
}

// TestGateEvaluateRefusesWhenIdle: the same finding on an IDLE pool also refuses.
//
// This asserted the opposite until 2026-09-07, which made the feature inert: load is read
// from the server, ordinary commands run without a persistent one, so the idle branch was
// the one every real redundant gate took.
func TestGateEvaluateRefusesWhenIdle(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	seedGreenGate(t, root, "fp-1")

	g := &GateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1", scope: sessions.GateScopeAffected}
	g.poolProbe = poolProbe(false)
	err := g.Evaluate(context.Background(), false)
	require.Error(t, err, "an idle machine is not a reason to re-verify a passed gate")
	assert.Contains(t, err.Error(), "re-verifies a gate that already passed",
		"the refusal names redundancy, not load, as its reason")
	assert.Contains(t, err.Error(), "--no-redundancy-check", "and names the override")
}

// TestGateEvaluateInertWithoutRecord: the first gate on a branch always runs,
// even under load, with no output from the feature at all.
func TestGateEvaluateInertWithoutRecord(t *testing.T) {
	testkit.Isolate(t)

	g := &GateRedundancy{root: t.TempDir(), target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1"}
	g.poolProbe = poolProbe(true)
	require.NoError(t, g.Evaluate(context.Background(), false))
}

// TestGateEvaluateOverride: --no-redundancy-check runs under load with a green
// gate on record.
func TestGateEvaluateOverride(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	seedGreenGate(t, root, "fp-1")

	g := &GateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1", scope: sessions.GateScopeAffected}
	g.poolProbe = poolProbe(true)
	require.NoError(t, g.Evaluate(context.Background(), true))
}

// TestGateEvaluateOtherBranch: a record for another branch says nothing about
// this one.
func TestGateEvaluateOtherBranch(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	seedGreenGate(t, root, "fp-1")

	g := &GateRedundancy{root: root, target: types.TargetCI, ref: "other", commit: "c1", fp: "fp-1"}
	g.poolProbe = poolProbe(true)
	require.NoError(t, g.Evaluate(context.Background(), false))
}

// TestGateEvaluateFailedRecord: a recorded FAIL never defers, whatever the
// fingerprint says.
func TestGateEvaluateFailedRecord(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	require.NoError(t, sessions.RecordGate(dir, sessions.GateResult{
		Target: types.TargetCI, Scope: sessions.GateScopeAffected, Ref: "b", Commit: "c1",
		Outcome: sessions.OutcomeFail, Fingerprint: "fp-1",
	}, sessions.InvocationStart{Workspace: root}))

	g := &GateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1", scope: sessions.GateScopeAffected}
	g.poolProbe = poolProbe(true)
	require.NoError(t, g.Evaluate(context.Background(), false))
}

// TestGateEvaluateNestedNeverRefuses: a magus under another magus reads its
// own ancestors' claims as load, so it advises instead of refusing.
func TestGateEvaluateNestedNeverRefuses(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	seedGreenGate(t, root, "fp-1")

	g := &GateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1", scope: sessions.GateScopeAffected, nested: true}
	g.poolProbe = poolProbe(true)
	require.NoError(t, g.Evaluate(context.Background(), false))
}

// TestGateRenderFindingDelta pins the every-file block a delta refusal or
// advisory prints: one line per path with the class and the declaration that
// classified it, so a reader disputes the decision line by line.
func TestGateRenderFindingDelta(t *testing.T) {
	g := &GateRedundancy{target: types.TargetCI, ref: "b", fp: "fp-2"}
	f := gateFinding{
		rec: sessions.GateRecord{
			GateResult: sessions.GateResult{Ref: "b", Commit: "c1", Fingerprint: "fp-1", Inv: "inv123"},
			At:         time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC),
		},
		delta: types.TierReport{Evidence: []check.TierEvidence{
			{Path: "docs/a.md", Class: "prose", Tier: check.RiskTrivial, Why: `matches "**/*.md" (built-in default); nothing in ci's chain reads it`},
			{Path: "gen/kg.json", Class: "generated", Tier: check.RiskTrivial, Why: "generated by .:kg, and nothing it reads changed"},
		}}.Lines(),
	}
	got := g.renderFinding(f)
	assert.Contains(t, got, "green gate: run inv123, branch b, commit c1, recorded 2026-09-03T10:00:00Z")
	assert.Contains(t, got, "delta since that gate, every file:")
	assert.Contains(t, got, `docs/a.md: trivial (prose: matches "**/*.md" (built-in default); nothing in ci's chain reads it)`, "names the classifying glob and its origin")
	assert.Contains(t, got, "gen/kg.json: trivial (generated: generated by .:kg, and nothing it reads changed)")
	lines := strings.Split(got, "\n")
	assert.Len(t, lines, 4, "one line per file, plus the header lines; never a summary")
}

// TestRenderSizing pins the sized gate's stderr block: the decision and its override,
// every path with its tier, and the commands that run instead.
func TestRenderSizing(t *testing.T) {
	t.Parallel()

	rep := types.TierReport{
		Base: "0123456789abcdef", Tier: check.RiskMechanical,
		Evidence: []check.TierEvidence{
			{Path: "run.go", Class: "comment-only", Tier: check.RiskMechanical, Why: "only comments differ; lint and generators read comments"},
		},
		Gate: []types.TierGateStep{
			{Target: "lint", Projects: []string{"."}, Argv: []string{"magus", "run", "lint", ".", "--no-default-charms"}},
			{Target: "generate", Projects: []string{"docs"}, Argv: []string{"magus", "run", "generate", "docs", "--no-default-charms"}},
		},
	}
	assert.Equal(t, "ci gate sized mechanical against 01234567; override: --no-redundancy-check\n"+
		"  run.go: mechanical (comment-only: only comments differ; lint and generators read comments)\n"+
		"  gate: magus run lint . --no-default-charms && magus run generate docs --no-default-charms",
		RenderSizing(types.TargetCI, rep))

	rep = types.TierReport{Base: "c1", Tier: check.RiskTrivial, Gate: []types.TierGateStep{},
		Evidence: []check.TierEvidence{{Path: "notes.md", Class: "prose", Tier: check.RiskTrivial, Why: "prose"}}}
	assert.Equal(t, "ci gate sized trivial against c1; override: --no-redundancy-check\n"+
		"  notes.md: trivial (prose: prose)\n"+
		"  gate: none", RenderSizing(types.TargetCI, rep))

	rep = types.TierReport{Base: "c1", Tier: check.RiskScoped,
		Gate: []types.TierGateStep{{Target: "test", Projects: []string{"."}, Op: "go::go-test", Packages: []string{"fx/a", "fx/b"},
			Argv: []string{"magus", "run", "test", ".", "--no-default-charms"}}}}
	assert.Equal(t, "ci gate sized scoped against c1; override: --no-redundancy-check\n"+
		"  gate: magus run test . --no-default-charms\n"+
		"  narrowed: go::go-test in . test runs 2 package(s): fx/a fx/b", RenderSizing(types.TargetCI, rep))
}

// TestGateSizeInert: sizing is off for an inert gate and a forced run, before anything
// is assessed.
func TestGateSizeInert(t *testing.T) {
	t.Parallel()

	var absent *GateRedundancy
	assert.Nil(t, absent.Size(context.Background(), "", false))
	g := &GateRedundancy{target: types.TargetCI}
	assert.Nil(t, g.Size(context.Background(), "", true), "--no-redundancy-check runs the full gate")
	assert.Empty(t, g.tier)
}

// TestGateRecordsUndeclaredSeeds: the gate verdict says whether the branch is green, and
// nothing about what it cost to say so. The projects only an undeclared changed file put
// in the set are the part of that cost whose answer could not have moved, so they ride the
// record and stay countable across a branch. Nil-safe like every other method here: a
// non-ci or dry run has no gate to note anything on.
func TestGateRecordsUndeclaredSeeds(t *testing.T) {
	t.Parallel()

	var absent *GateRedundancy
	absent.NoteUndeclaredSeeds([]string{"."})

	g := &GateRedundancy{target: types.TargetCI, ref: "b", commit: "c1", projects: []string{".", "docs"}}
	g.NoteUndeclaredSeeds([]string{"."})
	assert.Equal(t, []string{"."}, g.undeclared)
}

// TestGateRenderFindingNamesTheUndeclaredSeeds: a refusal explains why this run adds
// nothing. When the gate it defers to itself paid for projects nothing declares, that is
// the same subject and the reader is already here.
func TestGateRenderFindingNamesTheUndeclaredSeeds(t *testing.T) {
	t.Parallel()

	g := &GateRedundancy{target: types.TargetCI, ref: "b", fp: "fp-2"}
	f := gateFinding{rec: sessions.GateRecord{GateResult: sessions.GateResult{
		Ref: "b", Commit: "c1", Fingerprint: "fp-1", Inv: "inv123",
		UndeclaredSeeds: []string{".", "docs"},
	}}, identical: true}

	got := g.renderFinding(f)
	assert.Contains(t, got, "that gate covered 2 project(s) selected only by files nothing declares (MGS1028): ., docs")

	f.rec.UndeclaredSeeds = nil
	assert.NotContains(t, g.renderFinding(f), "MGS1028", "a gate with nothing undeclared-only says nothing")
}

// TestGateRecordsNothingWhenTheRunWasCutShort is the regression for a gate that recorded
// itself GREEN after a terminating shell killed it six seconds in: the run returned no
// error, because the targets it had dispatched reported their own cancellation and the
// run had nothing left to add, so testing runErr alone saw a clean finish. MGS3010 then
// refused every later gate on that commit, citing a pass over nine projects nothing ran.
func TestGateRecordsNothingWhenTheRunWasCutShort(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)

	g := &GateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1", scope: sessions.GateScopeAffected}

	cut, cancel := context.WithCancel(context.Background())
	cancel()
	g.Record(cut, nil, true /* cutShort */, nil)

	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	_, ok := sessions.LatestGate(fold, "b", types.TargetCI)
	assert.False(t, ok, "a cancelled run must record no verdict, whatever it returned")

	g.Record(context.Background(), nil, false, nil)
	rec, ok := sessions.LatestGate(fold2(t, dir), "b", types.TargetCI)
	require.True(t, ok, "a run that finished still records")
	assert.Equal(t, sessions.OutcomePass, rec.Outcome)
}

// TestGateRecordsAVerdictDecidedBeforeTheSignal is the other half, and the reason
// cutShort is the CALLER's answer rather than a context read inside record. A gate that
// completes and is then interrupted has already decided; dropping that verdict costs a
// full re-run of a gate that genuinely passed. The context is cancelled here to prove the
// decision no longer turns on it.
func TestGateRecordsAVerdictDecidedBeforeTheSignal(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)

	g := &GateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1", scope: sessions.GateScopeAffected}

	interrupted, cancel := context.WithCancel(context.Background())
	cancel()
	g.Record(interrupted, nil, false /* the run had already returned */, nil)

	rec, ok := sessions.LatestGate(fold2(t, dir), "b", types.TargetCI)
	require.True(t, ok, "a verdict decided before the signal must survive it")
	assert.Equal(t, sessions.OutcomePass, rec.Outcome)
}

// fold2 re-reads the store, since a Fold is a snapshot rather than a live view.
func fold2(t *testing.T, dir string) sessions.Fold {
	t.Helper()
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	return fold
}

// repairedGate is a gate whose store holds a repairing run that left a.txt, with probe
// standing in for the checkout comparison.
func repairedGate(t *testing.T, probe func(context.Context, types.VCSDriver, string, []types.RepairedFile) ([]types.RepairedFile, error)) *GateRedundancy {
	t.Helper()
	testkit.Isolate(t)
	g := &GateRedundancy{root: t.TempDir(), drv: stubDriver{}, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1",
		scope: sessions.GateScopeAffected, repairs: probe}
	g.Record(context.Background(), &types.UncommittedRepairError{
		Files:      []types.RepairedFile{{Path: "a.txt", Digest: "d"}},
		Diagnostic: types.DiagnosticErrorf(types.UncommittedRepair, "ci repaired 1 file"),
	}, false, nil)
	return g
}

type stubDriver struct{ types.VCSDriver }

func TestGateRefusesWhileTheProbeHoldsARepair(t *testing.T) {
	g := repairedGate(t, func(_ context.Context, _ types.VCSDriver, _ string, files []types.RepairedFile) ([]types.RepairedFile, error) {
		return files, nil
	})

	err := g.Evaluate(context.Background(), false)
	require.ErrorIs(t, err, types.UncommittedRepair)
	assert.Contains(t, err.Error(), "repaired 1 file still uncommitted: a.txt")
	assert.NotEqual(t, exit.TempFail, exit.Code(err), "a refusal to verify uncommitted bytes is no deferral")
	assert.Error(t, g.Evaluate(context.Background(), true), "the refusal is no redundancy check")
}

func TestGateRunsOnceTheProbeHoldsNoRepair(t *testing.T) {
	g := repairedGate(t, func(context.Context, types.VCSDriver, string, []types.RepairedFile) ([]types.RepairedFile, error) {
		return nil, nil
	})

	assert.NoError(t, g.Evaluate(context.Background(), false))
}

func TestGateRunsWhenTheRepairProbeIsMissingOrFails(t *testing.T) {
	g := repairedGate(t, nil)
	require.NoError(t, g.Evaluate(context.Background(), false), "an unanswered check is said and the gate runs")

	g = repairedGate(t, func(context.Context, types.VCSDriver, string, []types.RepairedFile) ([]types.RepairedFile, error) {
		return nil, errors.New("no checkout")
	})
	assert.NoError(t, g.Evaluate(context.Background(), false))
}

func TestDescribePoolSaysWhatItSaw(t *testing.T) {
	saturated, state := DescribePool(&budget.Snapshot{BudgetSlots: 8, HeldSlots: 8})
	assert.True(t, saturated)
	assert.Equal(t, "saturated: 8 of 8 slots held", state)

	saturated, state = DescribePool(&budget.Snapshot{BudgetSlots: 8, HeldSlots: 2})
	assert.False(t, saturated)
	assert.Equal(t, "idle: 2 of 8 slots held", state)
}

func TestIsGateInvocationIsTheWholeExecutingCITarget(t *testing.T) {
	assert.True(t, IsGateInvocation(types.TargetCI, false, false))
	assert.False(t, IsGateInvocation(types.TargetCI, true, false), "a shard is not the gate")
	assert.False(t, IsGateInvocation(types.TargetCI, false, true), "a dry run executes nothing")
	assert.False(t, IsGateInvocation("test", false, false))
}

func TestCIRecordSuite(t *testing.T) { suite.Run(t, new(CIRecordSuite)) }

// CIRecordSuite pins what a ci run's verdict record says.
type CIRecordSuite struct{ suite.Suite }

// tidyRepair is a.txt as the repair left it, with its digest.
var tidyRepair = types.RepairedFile{Path: "a.txt", Digest: hexDigest("tidy\n")}

func hexDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// repairedCheckout commits a.txt untidy, leaves it repaired and uncommitted,
// and records the ci run that repaired it.
func (s *CIRecordSuite) repairedCheckout() (*testkit.Repo, *GateRedundancy) {
	testkit.Isolate(s.T())
	repo := testkit.NewRepository(s.T(), map[string]string{".gitignore": ".magus/\n", "a.txt": "messy\n"})
	s.write(repo, "a.txt", "tidy\n")
	g := s.gate(repo, "b")
	g.Record(context.Background(), &types.UncommittedRepairError{
		Writers:    []types.TargetRef{{Project: ".", Target: "tidy"}},
		Files:      []types.RepairedFile{tidyRepair},
		Diagnostic: types.DiagnosticErrorf(types.UncommittedRepair, "ci repaired 1 file through .:tidy: a.txt"),
	}, false, nil)
	return repo, g
}

func (s *CIRecordSuite) gate(repo *testkit.Repo, ref string) *GateRedundancy {
	res, err := vcs.Resolve(s.T().Context(), repo.Root(), "", types.VCSOptions{})
	s.Require().NoError(err)
	return &GateRedundancy{
		root: repo.Root(), drv: res.VCS, target: types.TargetCI, ref: ref, commit: repo.Head(),
		fp: "fp-1", projects: []string{"."}, scope: sessions.GateScopeAffected, repairs: dirtyRepairs,
	}
}

func (s *CIRecordSuite) write(repo *testkit.Repo, rel, content string) {
	s.Require().NoError(os.WriteFile(repo.Path(rel), []byte(content), 0o644))
}

func (s *CIRecordSuite) records(root string) int {
	dir, err := sessions.Dir(root)
	s.Require().NoError(err)
	return len(fold2(s.T(), dir).Records)
}

func (s *CIRecordSuite) TestRepairedRunRecordsAFailureWithEachFilesDigest() {
	repo, _ := s.repairedCheckout()
	dir, err := sessions.Dir(repo.Root())
	s.Require().NoError(err)

	rec, ok := sessions.LatestRepair(fold2(s.T(), dir), types.TargetCI)
	s.Require().True(ok)
	s.Equal(sessions.GateRecord{
		GateResult: sessions.GateResult{
			Target:        types.TargetCI,
			Scope:         sessions.GateScopeAffected,
			Ref:           "b",
			Commit:        repo.Head(),
			Outcome:       sessions.OutcomeFail,
			Fingerprint:   "fp-1",
			Projects:      []string{"."},
			RepairedFiles: []types.RepairedFile{tidyRepair},
		},
		At: rec.At,
	}, rec, "HEAD's bytes failed, and the record holds each repaired file's digest")
}

func (s *CIRecordSuite) TestRerunWithUncommittedRepairsRefuses() {
	_, g := s.repairedCheckout()

	err := g.Evaluate(context.Background(), false)
	s.Require().True(errors.Is(err, types.UncommittedRepair), "got %v", err)
	s.Contains(err.Error(), "repaired 1 file still uncommitted: a.txt")
	s.NotEqual(exit.TempFail, exit.Code(err), "a refusal to verify uncommitted bytes is no deferral")
}

func (s *CIRecordSuite) TestCommitThatLeavesTheRepairsOutStillRefuses() {
	repo, _ := s.repairedCheckout()
	s.write(repo, "b.txt", "b\n")
	repo.Git("add", "b.txt")
	repo.Git("commit", "-q", "-m", "b")

	s.Error(s.gate(repo, "b").Evaluate(context.Background(), false))
}

func (s *CIRecordSuite) TestRerunAfterTheCommitRuns() {
	repo, _ := s.repairedCheckout()
	repo.Commit("tidy", nil)

	s.NoError(s.gate(repo, "b").Evaluate(context.Background(), false))
}

func (s *CIRecordSuite) TestRerunAfterTheRepairsAreDiscardedRuns() {
	repo, g := s.repairedCheckout()
	repo.Git("checkout", "--", "a.txt")

	s.NoError(g.Evaluate(context.Background(), false))
}

func (s *CIRecordSuite) TestRepairedFileEditedFurtherDoesNotRefuse() {
	repo, g := s.repairedCheckout()
	s.write(repo, "a.txt", "tidy, then edited\n")

	s.NoError(g.Evaluate(context.Background(), false))
}

func (s *CIRecordSuite) TestRefusalIgnoresNoRedundancyCheck() {
	_, g := s.repairedCheckout()

	s.Error(g.Evaluate(context.Background(), true), "the refusal is no redundancy check")
}

func (s *CIRecordSuite) TestOtherWorktreesRepairsDoNotRefuse() {
	repo, _ := s.repairedCheckout()
	other := repo.AddWorktree("other")

	s.NoError(s.gate(other, "other").Evaluate(context.Background(), false))
}

func (s *CIRecordSuite) TestBranchSwitchAtTheSameCommitStillRefuses() {
	repo, _ := s.repairedCheckout()
	repo.Git("switch", "-q", "-c", "feature")

	s.Error(s.gate(repo, "feature").Evaluate(context.Background(), false))
}

func (s *CIRecordSuite) TestRefusalRecordsNothing() {
	repo, g := s.repairedCheckout()
	before := s.records(repo.Root())

	s.Require().Error(g.Evaluate(context.Background(), false))
	s.Equal(before, s.records(repo.Root()), "a refusal's own record would shadow the repair's")
}

// dirtyRepairs stands in for the checkout comparison magus.UncommittedRepairs makes: a
// recorded file is held while git lists it dirty and it still has its recorded digest.
func dirtyRepairs(_ context.Context, _ types.VCSDriver, dir string, files []types.RepairedFile) ([]types.RepairedFile, error) {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return nil, err
	}
	var held []types.RepairedFile
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		for _, f := range files {
			content, err := os.ReadFile(filepath.Join(dir, f.Path))
			if err != nil {
				return nil, err
			}
			if line[3:] == f.Path && hexDigest(string(content)) == f.Digest {
				held = append(held, f)
			}
		}
	}
	return held, nil
}
