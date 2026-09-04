package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	internalci "github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/sessions"
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
	}, sessions.SessionStart{Workspace: root}))
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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
// verdict - the next evaluation still finds it and still refuses.
func TestGateRefusalRecordsDeferral(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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

// TestGateEvaluateAdvisesWhenIdle: the same finding on an idle pool runs.
func TestGateEvaluateAdvisesWhenIdle(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("MAGUS_LEVEL", "0")
	root := t.TempDir()
	seedGreenGate(t, root, "fp-1")
	stubPool(t, false)

	g := &gateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1"}
	assert.NoError(t, g.evaluate(context.Background(), false))
}

// TestGateEvaluateInertWithoutRecord: the first gate on a branch always runs,
// even under load, with no output from the feature at all.
func TestGateEvaluateInertWithoutRecord(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("MAGUS_LEVEL", "0")
	stubPool(t, true)

	g := &gateRedundancy{root: t.TempDir(), target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1"}
	assert.NoError(t, g.evaluate(context.Background(), false))
}

// TestGateEvaluateOverride: --no-redundancy-check runs under load with a green
// gate on record.
func TestGateEvaluateOverride(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("MAGUS_LEVEL", "0")
	root := t.TempDir()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	require.NoError(t, sessions.RecordGate(dir, sessions.GateResult{
		Target: types.TargetCI, Ref: "b", Commit: "c1",
		Outcome: sessions.OutcomeFail, Fingerprint: "fp-1",
	}, sessions.SessionStart{Workspace: root}))
	stubPool(t, true)

	g := &gateRedundancy{root: root, target: types.TargetCI, ref: "b", commit: "c1", fp: "fp-1"}
	assert.NoError(t, g.evaluate(context.Background(), false))
}

// TestGateEvaluateNestedNeverRefuses: a magus under another magus reads its
// own ancestors' claims as load, so it advises instead of refusing.
func TestGateEvaluateNestedNeverRefuses(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
		delta: internalci.GateDelta{Paths: []internalci.ClassifiedPath{
			{Path: "docs/a.md", Class: internalci.ClassProse, Why: `matches "**/*.md" (built-in default)`},
			{Path: "gen/kg.json", Class: internalci.ClassGenerated, Why: "a declared output glob claims it"},
			{Path: "run.go", Class: internalci.ClassCommentOnly, Why: "only comments differ from the green gate's revision"},
		}},
	}
	got := g.renderFinding(f)
	assert.Contains(t, got, "green gate: run inv123, branch b, commit c1, recorded 2026-09-03T10:00:00Z")
	assert.Contains(t, got, "delta since that gate, every file:")
	assert.Contains(t, got, `docs/a.md: prose (matches "**/*.md" (built-in default))`, "names the classifying glob and its origin")
	assert.Contains(t, got, "gen/kg.json: generated (a declared output glob claims it)")
	assert.Contains(t, got, "run.go: comment-only (only comments differ from the green gate's revision)")
	lines := strings.Split(got, "\n")
	assert.Len(t, lines, 5, "one line per file, plus the header lines; never a summary")
}
