package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedGreenGate records a passing ci gate for branch b with fingerprint fp in
// the session store XDG_STATE_HOME points at.
func seedGreenGate(t *testing.T, root, fp string) {
	t.Helper()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	require.NoError(t, sessions.RecordGate(dir, sessions.GateResult{
		Target: types.TargetCI, Ref: "b", Commit: "c1",
		Outcome: sessions.OutcomePass, Fingerprint: fp,
		Projects: []string{"."}, Charms: []string{"quiet"},
		Inv: "inv123",
	}, sessions.InvocationStart{Workspace: root}))
}

// stubPool pins the load answer for one test, restoring the real probe after.
func stubPool(t *testing.T, saturated bool) {
	t.Helper()
	prev := gatePoolProbe
	state := "idle: 2 of 8 slots held"
	if saturated {
		state = "saturated: 8 of 8 slots held, 2 runs queued"
	}
	gatePoolProbe = func(context.Context) (bool, string) { return saturated, state }
	t.Cleanup(func() { gatePoolProbe = prev })
}

// TestGateEvaluateRefusesUnderLoad is the refusal: a recorded green gate with
// identical inputs plus a saturated pool exits 75 with MGS3010. The message
// must let a reader reconstruct the decision: the green gate's run ref, branch
// and timestamp, the finding, the pool state, the override, the alternative.
func TestGateEvaluateRefusesUnderLoad(t *testing.T) {
	testkit.Isolate(t)
	t.Setenv("MAGUS_LEVEL", "0")
	root := t.TempDir()
	seedGreenGate(t, root, "fp-1")
	stubPool(t, true)

	g := &gateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1"}
	err := g.evaluate(context.Background(), false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.RedundantGateDeferred), "the refusal carries MGS3010")
	code, ok := proc.ExitCode(err)
	require.True(t, ok, "the refusal states its own exit code")
	assert.Equal(t, 75, code, "EX_TEMPFAIL, the same convention as MGS3009")

	msg := err.Error()
	assert.Contains(t, msg, "run inv123", "names the green gate's run ref")
	assert.Contains(t, msg, "branch b", "names the branch")
	assert.Contains(t, msg, "commit c1", "names the green gate's commit")
	assert.Contains(t, msg, "recorded 20", "carries the RFC3339 timestamp")
	assert.Contains(t, msg, "matches it exactly", "names the identical-fingerprint finding")
	assert.Contains(t, msg, "machine pool: saturated: 8 of 8 slots held, 2 runs queued", "prints the pool state it saw")
	assert.Contains(t, msg, "--no-redundancy-check", "names the override")
	assert.Contains(t, msg, "pull request runs the identical check", "names the alternative")
}

// TestGateRefusalRecordsDeferral: the refusal persists as a deferred gate
// record pointing at the green gate, and that record never shadows the green
// verdict: the next evaluation still finds it and still refuses.
func TestGateRefusalRecordsDeferral(t *testing.T) {
	testkit.Isolate(t)
	t.Setenv("MAGUS_LEVEL", "0")
	root := t.TempDir()
	seedGreenGate(t, root, "fp-1")
	stubPool(t, true)

	g := &gateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c2", fp: "fp-1", projects: []string{"."}}
	require.Error(t, g.evaluate(context.Background(), false))

	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	rec, ok := sessions.LatestGate(fold, "b", types.TargetCI)
	require.True(t, ok)
	assert.Equal(t, sessions.OutcomePass, rec.Outcome, "the deferral does not shadow the green verdict")
	assert.Equal(t, "c1", rec.Commit)

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
	assert.Equal(t, "c1", deferred.DeferredTo, "pointing at the green gate it deferred to")
	assert.Equal(t, "c2", deferred.Commit, "at the commit the refused run was at")

	require.Error(t, g.evaluate(context.Background(), false), "still refuses after its own deferral record")
}

// TestGateEvaluateRefusesWhenIdle: the same finding on an IDLE pool also refuses.
//
// This asserted the opposite until 2026-09-07, which made the feature inert: load is read
// from the server, ordinary commands run without a persistent one, so the idle branch was
// the one every real redundant gate took.
func TestGateEvaluateRefusesWhenIdle(t *testing.T) {
	testkit.Isolate(t)
	t.Setenv("MAGUS_LEVEL", "0")
	root := t.TempDir()
	seedGreenGate(t, root, "fp-1")
	stubPool(t, false)

	g := &gateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1"}
	err := g.evaluate(context.Background(), false)
	require.Error(t, err, "an idle machine is not a reason to re-verify a passed gate")
	assert.Contains(t, err.Error(), "re-verifies a gate that already passed",
		"the refusal names redundancy, not load, as its reason")
	assert.Contains(t, err.Error(), "--no-redundancy-check", "and names the override")
}

// TestGateEvaluateInertWithoutRecord: the first gate on a branch always runs,
// even under load, with no output from the feature at all.
func TestGateEvaluateInertWithoutRecord(t *testing.T) {
	testkit.Isolate(t)
	t.Setenv("MAGUS_LEVEL", "0")
	stubPool(t, true)

	g := &gateRedundancy{root: t.TempDir(), target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1"}
	assert.NoError(t, g.evaluate(context.Background(), false))
}

// TestGateEvaluateOverride: --no-redundancy-check runs under load with a green
// gate on record.
func TestGateEvaluateOverride(t *testing.T) {
	testkit.Isolate(t)
	t.Setenv("MAGUS_LEVEL", "0")
	root := t.TempDir()
	seedGreenGate(t, root, "fp-1")
	stubPool(t, true)

	g := &gateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1"}
	assert.NoError(t, g.evaluate(context.Background(), true))
}

// TestGateEvaluateOtherBranch: a record for another branch says nothing about
// this one.
func TestGateEvaluateOtherBranch(t *testing.T) {
	testkit.Isolate(t)
	t.Setenv("MAGUS_LEVEL", "0")
	root := t.TempDir()
	seedGreenGate(t, root, "fp-1")
	stubPool(t, true)

	g := &gateRedundancy{root: root, target: types.TargetCI, ref: "other", commit: "c1", fp: "fp-1"}
	assert.NoError(t, g.evaluate(context.Background(), false))
}

// TestGateEvaluateFailedRecord: a recorded FAIL never defers, whatever the
// fingerprint says.
func TestGateEvaluateFailedRecord(t *testing.T) {
	testkit.Isolate(t)
	t.Setenv("MAGUS_LEVEL", "0")
	root := t.TempDir()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	require.NoError(t, sessions.RecordGate(dir, sessions.GateResult{
		Target: types.TargetCI, Ref: "b", Commit: "c1",
		Outcome: sessions.OutcomeFail, Fingerprint: "fp-1",
	}, sessions.InvocationStart{Workspace: root}))
	stubPool(t, true)

	g := &gateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1"}
	assert.NoError(t, g.evaluate(context.Background(), false))
}

// TestGateEvaluateNestedNeverRefuses: a magus under another magus reads its
// own ancestors' claims as load, so it advises instead of refusing.
func TestGateEvaluateNestedNeverRefuses(t *testing.T) {
	testkit.Isolate(t)
	t.Setenv("MAGUS_LEVEL", "1")
	root := t.TempDir()
	seedGreenGate(t, root, "fp-1")
	stubPool(t, true)

	g := &gateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1"}
	assert.NoError(t, g.evaluate(context.Background(), false))
}

// TestGateRenderFindingDelta pins the every-file block a delta refusal or
// advisory prints: one line per path with the class and the declaration that
// classified it, so a reader disputes the decision line by line.
func TestGateRenderFindingDelta(t *testing.T) {
	g := &gateRedundancy{target: types.TargetCI, ref: "b", fp: "fp-2"}
	f := gateFinding{
		rec: sessions.GateRecord{
			GateResult: sessions.GateResult{Ref: "b", Commit: "c1", Fingerprint: "fp-1", Inv: "inv123"},
			At:         time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC),
		},
		delta: types.RiskReport{Evidence: []types.RiskEvidence{
			{Path: "docs/a.md", Class: "prose", Tier: types.RiskTrivial, Why: `matches "**/*.md" (built-in default); nothing in ci's chain reads it`},
			{Path: "gen/kg.json", Class: "generated", Tier: types.RiskTrivial, Why: "generated by .:kg, and nothing it reads changed"},
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

	rep := types.RiskReport{
		Base: "0123456789abcdef", Tier: types.RiskMechanical,
		Evidence: []types.RiskEvidence{
			{Path: "run.go", Class: "comment-only", Tier: types.RiskMechanical, Why: "only comments differ; lint and generators read comments"},
		},
		Gate: []types.RiskGateStep{
			{Target: "lint", Projects: []string{"."}, Argv: []string{"magus", "run", "lint", ".", "--no-default-charms"}},
			{Target: "generate", Projects: []string{"docs"}, Argv: []string{"magus", "run", "generate", "docs", "--no-default-charms"}},
		},
	}
	assert.Equal(t, "magus: ci gate sized mechanical against 01234567; override: --no-redundancy-check\n"+
		"  run.go: mechanical (comment-only: only comments differ; lint and generators read comments)\n"+
		"  gate: magus run lint . --no-default-charms && magus run generate docs --no-default-charms\n",
		renderSizing(types.TargetCI, rep))

	rep = types.RiskReport{Base: "c1", Tier: types.RiskTrivial, Gate: []types.RiskGateStep{},
		Evidence: []types.RiskEvidence{{Path: "notes.md", Class: "prose", Tier: types.RiskTrivial, Why: "prose"}}}
	assert.Equal(t, "magus: ci gate sized trivial against c1; override: --no-redundancy-check\n"+
		"  notes.md: trivial (prose: prose)\n"+
		"  gate: none\n", renderSizing(types.TargetCI, rep))

	rep = types.RiskReport{Base: "c1", Tier: types.RiskScoped,
		Gate: []types.RiskGateStep{{Target: "test", Projects: []string{"."}, Op: "go::go-test", Packages: []string{"fx/a", "fx/b"},
			Argv: []string{"magus", "run", "test", ".", "--no-default-charms"}}}}
	assert.Equal(t, "magus: ci gate sized scoped against c1; override: --no-redundancy-check\n"+
		"  gate: magus run test . --no-default-charms\n"+
		"  narrowed: go::go-test in . test runs 2 package(s): fx/a fx/b\n", renderSizing(types.TargetCI, rep))
}

// TestGateSizeInert: sizing is off for an inert gate and a forced run, before anything
// is assessed.
func TestGateSizeInert(t *testing.T) {
	t.Parallel()

	var absent *gateRedundancy
	assert.Nil(t, absent.size(context.Background(), "", false))
	g := &gateRedundancy{target: types.TargetCI}
	assert.Nil(t, g.size(context.Background(), "", true), "--no-redundancy-check runs the full gate")
	assert.Empty(t, g.tier)
}

// TestGateRecordsUndeclaredSeeds: the gate verdict says whether the branch is green, and
// nothing about what it cost to say so. The projects only an undeclared changed file put
// in the set are the part of that cost whose answer could not have moved, so they ride the
// record and stay countable across a branch. Nil-safe like every other method here: a
// non-ci or dry run has no gate to note anything on.
func TestGateRecordsUndeclaredSeeds(t *testing.T) {
	t.Parallel()

	var absent *gateRedundancy
	absent.noteUndeclaredSeeds([]string{"."})

	g := &gateRedundancy{target: types.TargetCI, ref: "b", commit: "c1", projects: []string{".", "docs"}}
	g.noteUndeclaredSeeds([]string{"."})
	assert.Equal(t, []string{"."}, g.undeclared)
}

// TestGateRenderFindingNamesTheUndeclaredSeeds: a refusal explains why this run adds
// nothing. When the gate it defers to itself paid for projects nothing declares, that is
// the same subject and the reader is already here.
func TestGateRenderFindingNamesTheUndeclaredSeeds(t *testing.T) {
	t.Parallel()

	g := &gateRedundancy{target: types.TargetCI, ref: "b", fp: "fp-2"}
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

	g := &gateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1"}

	cut, cancel := context.WithCancel(context.Background())
	cancel()
	g.record(cut, nil, true /* cutShort */)

	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	_, ok := sessions.LatestGate(fold, "b", types.TargetCI)
	assert.False(t, ok, "a cancelled run must record no verdict, whatever it returned")

	g.record(context.Background(), nil, false)
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

	g := &gateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1"}

	interrupted, cancel := context.WithCancel(context.Background())
	cancel()
	g.record(interrupted, nil, false /* the run had already returned */)

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
