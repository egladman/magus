package guard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runLog writes one run log: a started event carrying argv and the version string, and a
// finished event carrying status. An empty status writes no finished event, which is what
// an interrupted run leaves behind.
func runLog(t *testing.T, dir, name, version, status string, argv ...string) {
	t.Helper()
	args := ""
	for i, a := range argv {
		if i > 0 {
			args += ","
		}
		args += `"` + a + `"`
	}
	body := `{"ts":1,"inv":"i","kind":"started","command":{"arguments":[` + args + `]},"magus_version":"` + version + `"}` + "\n"
	if status != "" {
		body += `{"ts":2,"inv":"i","kind":"finished","status":"` + status + `"}` + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
}

func TestGateCoverageReadsTheRunLog(t *testing.T) {
	t.Parallel()

	t.Run("a green gate at this commit covers the push", func(t *testing.T) {
		dir := t.TempDir()
		runLog(t, dir, "a.jsonl", "v0.4.3-97-gabc1234-dirty", "pass", "affected", "ci")
		assert.Equal(t, gatePassed, gateCoverageAt(dir, "abc1234"))
		decision, _ := gradePushWithoutGate(gateCoverageAt(dir, "abc1234"), "abc1234", "")
		assert.Empty(t, decision)
	})

	t.Run("a failed gate is not coverage", func(t *testing.T) {
		dir := t.TempDir()
		runLog(t, dir, "a.jsonl", "v0.4.3-97-gabc1234", "fail", "affected", "ci")
		assert.Equal(t, gateFailed, gateCoverageAt(dir, "abc1234"))
	})

	t.Run("an interrupted gate is not coverage", func(t *testing.T) {
		// No finished event, which is what a run killed at the terminal leaves. It is
		// not red and it is not green, and the deny must not call it either.
		dir := t.TempDir()
		runLog(t, dir, "a.jsonl", "v0.4.3-97-gabc1234", "", "run", "ci", ".")
		assert.Equal(t, gateIncomplete, gateCoverageAt(dir, "abc1234"))
		_, reason := gradePushWithoutGate(gateCoverageAt(dir, "abc1234"), "abc1234", "")
		assert.NotContains(t, reason, "RED")
	})

	t.Run("a green gate at a DIFFERENT commit is not coverage", func(t *testing.T) {
		// The case this rule exists for: gate green, commit, push. The work that moved
		// since is exactly what nothing has checked.
		dir := t.TempDir()
		runLog(t, dir, "a.jsonl", "v0.4.3-97-gdeadbee", "pass", "affected", "ci")
		assert.Equal(t, gateAbsent, gateCoverageAt(dir, "abc1234"))
		decision, _ := gradePushWithoutGate(gateCoverageAt(dir, "abc1234"), "abc1234", "")
		assert.Equal(t, "ask", decision)
	})

	t.Run("a green NON-gate run is not coverage", func(t *testing.T) {
		dir := t.TempDir()
		runLog(t, dir, "a.jsonl", "v0.4.3-97-gabc1234", "pass", "run", "go-build", ".")
		assert.Equal(t, gateAbsent, gateCoverageAt(dir, "abc1234"))
	})

	t.Run("one green gate among many runs is enough", func(t *testing.T) {
		dir := t.TempDir()
		runLog(t, dir, "a.jsonl", "v0.4.3-97-gabc1234", "fail", "affected", "ci")
		runLog(t, dir, "b.jsonl", "v0.4.3-97-gabc1234", "pass", "run", "go-build", ".")
		runLog(t, dir, "c.jsonl", "v0.4.3-97-gabc1234", "pass", "affected", "ci")
		assert.Equal(t, gatePassed, gateCoverageAt(dir, "abc1234"))
	})
}

// TestPushGateStandsDownWithNothingToProve pins that this rule refuses only on evidence.
//
// Every case where the question could not be ASKED has to pass, for the orchestrator and a
// leased worker alike: a fresh clone with no run log, a host with no VCS to read a revision from, a
// test fixture. Built without this distinction the rule denied all three, because an
// unasked question and a failed one shared the zero value.
func TestPushGateStandsDownWithNothingToProve(t *testing.T) {
	t.Parallel()

	for name, cover := range map[string]gateCoverage{
		"no run log":         gateCoverageAt("", "abc1234"),
		"no commit to match": gateCoverageAt(t.TempDir(), ""),
		"no such directory":  gateCoverageAt(filepath.Join(t.TempDir(), "absent"), "abc1234"),
	} {
		assert.Equal(t, gateUnknown, cover, name)
		for _, lease := range []string{"", "harness/worker"} {
			decision, reason := gradePushWithoutGate(cover, "abc1234", lease)
			assert.Empty(t, decision, "%s must neither ask nor deny (lease %q)", name, lease)
			assert.Empty(t, reason, name)
		}
	}
}

// TestBuiltFromMatchesEitherAbbreviation pins that the two sides may abbreviate the commit
// to different lengths: git describe picks its own, and the caller passes whatever the VCS
// layer gave it.
func TestBuiltFromMatchesEitherAbbreviation(t *testing.T) {
	t.Parallel()

	assert.True(t, builtFrom("v0.4.3-97-gabc1234-dirty", "abc1234"))
	assert.True(t, builtFrom("v0.4.3-97-gabc1234", "abc1234567"), "describe abbreviated shorter")
	assert.True(t, builtFrom("v0.4.3-97-gabc1234567", "abc1234"), "caller abbreviated shorter")
	assert.False(t, builtFrom("v0.4.3-97-gabc1234", "def5678"))
	assert.False(t, builtFrom("", "abc1234"))
	assert.False(t, builtFrom("v0.4.3-97-gabc1234", ""))
	assert.False(t, builtFrom("v0.4.3", "abc1234"), "a release build names no commit")
}

// backendRevisions are the ids each backend's Metadata reports for one revision: the Short
// the guard reads as HEAD, and the full ID a gate records. Formats are the backends' own:
// git's 7-hex describe abbreviation, hg and Sapling's `{short(node)}` / `{node|short}`
// (12 hex) against the 40-hex node, and jj's `commit_id.short()` (12 hex) against commit_id.
var backendRevisions = map[string]struct{ short, full string }{
	"git":     {"5d17aa4", "5d17aa4e3c2b1f0a9d8c7b6a5f4e3d2c1b0a9f8e"},
	"hg":      {"4a1c0cc84b21", "4a1c0cc84b21f0e9d8c7b6a5f4e3d2c1b0a9f8e7"},
	"sapling": {"9e8d7c6b5a41", "9e8d7c6b5a41f2e3d4c5b6a7980e1f2a3b4c5d6e"},
	"jj":      {"c0ffee12ab34", "c0ffee12ab34d56e78f90a1b2c3d4e5f60718293"},
}

// TestGateMatchesEachBackendsRevision pins that a gate recorded at a revision's full id
// covers a push judged at that revision's short id, on every backend, through both the
// session store and the run log.
func TestGateMatchesEachBackendsRevision(t *testing.T) {
	for backend, rev := range backendRevisions {
		t.Run(backend, func(t *testing.T) {
			testkit.Isolate(t)
			root := t.TempDir()
			dir, err := sessions.Dir(root)
			require.NoError(t, err)
			require.NoError(t, sessions.RecordGate(dir, sessions.GateResult{
				Target: types.TargetCI, Commit: rev.full, Outcome: sessions.OutcomePass,
			}, sessions.InvocationStart{Workspace: root}))
			assert.Equal(t, gatePassed, gateVerdictAt(root, rev.short))

			runs := t.TempDir()
			runLog(t, runs, "a.jsonl", "v0.4.3-1-g"+rev.full, "pass", "affected", "ci")
			assert.Equal(t, gatePassed, gateCoverageAt(runs, rev.short))
		})
	}
}

// TestGateStandsDownOnARevisionItCannotMatch pins the other half: an id that is not a
// content hash cannot be prefix-matched against one, so the rule reports unknown and stands
// down rather than matching by accident or asking on no evidence. hg's local revision
// numbers are small integers that prefix-match any node starting with those digits, and a
// jj change id is spelled in k-z, never hex.
func TestGateStandsDownOnARevisionItCannotMatch(t *testing.T) {
	for name, id := range map[string]string{
		"hg local revision number": "42",
		"jj change id":             "zyxwvutsrqpo",
		"too short to be a prefix": "4a1",
	} {
		t.Run(name, func(t *testing.T) {
			runs := t.TempDir()
			runLog(t, runs, "a.jsonl", "v0.4.3-1-g42a1c0cc84b21f0e9d8c7b6a5f4e3d2c1b0a9f8", "pass", "affected", "ci")
			assert.Equal(t, gateUnknown, gateCoverageAt(runs, id))
			assert.False(t, builtFrom("v0.4.3-1-g42a1c0cc84b21f0e9d8c7b6a5f4e3d2c1b0a9f8", id))

			testkit.Isolate(t)
			root := t.TempDir()
			dir, err := sessions.Dir(root)
			require.NoError(t, err)
			require.NoError(t, sessions.RecordGate(dir, sessions.GateResult{
				Target: types.TargetCI, Commit: "42a1c0cc84b21f0e9d8c7b6a5f4e3d2c1b0a9f8", Outcome: sessions.OutcomeFail,
			}, sessions.InvocationStart{Workspace: root}))
			assert.Equal(t, gateUnknown, gateVerdictAt(root, id))
		})
	}
}

// judgePush runs one push through Judge in a workspace whose run log exists, so a gate
// missing at HEAD is proven absent rather than unknown. gate, when set, records a finished
// `affected ci` run with that status at HEAD first.
func judgePush(t *testing.T, gate, lease string, leases ...types.Job) Verdict {
	t.Helper()
	return judgePushFrom(t, true, gate, lease, leases...)
}

// judgePushFrom is judgePush for a caller that does or does not declare it renders ask.
func judgePushFrom(t *testing.T, rendersAsk bool, gate, lease string, leases ...types.Job) Verdict {
	t.Helper()
	ctx, _ := fleetFixture(t, leases...)
	runs := filepath.Join(hookLocation(ctx, Dependencies{}).cacheDir, cache.RunsDir)
	require.NoError(t, os.MkdirAll(runs, 0o755))
	if gate != "" {
		runLog(t, runs, "a.jsonl", "v0.4.3-97-gabc1234", gate, "affected", "ci")
	}
	deps := Dependencies{HeadCommit: func(context.Context) string { return "abc1234" }}
	return Judge(ctx, deps, Request{Input: "git push origin HEAD", Lease: lease, RendersAsk: rendersAsk})
}

// TestAskReachesOnlyACallerThatRendersIt pins the fail-open this closes: a hook installed
// before the decision existed renders an ask as nothing, and every host reads nothing as
// allow. A caller that does not declare it renders ask gets a deny that says why.
func TestAskReachesOnlyACallerThatRendersIt(t *testing.T) {
	v := judgePushFrom(t, false, "", "")
	assert.Equal(t, "deny", v.Decision)
	assert.Equal(t, string(denyRulePushUngated), v.Rule)
	assert.Contains(t, v.Reason, "predates approval prompts")
	assert.Contains(t, v.Reason, "magus agent harness apply")
	assert.Contains(t, v.Reason, "abc1234", "the refusal still names what it refused")

	gated := judgePushFrom(t, false, "pass", "")
	assert.Contains(t, []string{"pass", "advise"}, gated.Decision, "a covered push needs no prompt, so an old hook passes it")
}

// TestUngatedPushAsksTheOrchestrator pins that consent to publish comes from the person,
// through the host's own prompt. A marker the agent types is not consent, and the deny this
// replaced promised exactly that escape without implementing it.
func TestUngatedPushAsksTheOrchestrator(t *testing.T) {
	v := judgePush(t, "", "")
	assert.Equal(t, "ask", v.Decision)
	assert.Equal(t, string(denyRulePushUngated), v.Rule)
	assert.Contains(t, v.Reason, "abc1234", "the prompt names the commit it publishes")
	assert.Contains(t, v.Reason, "no `ci` run is recorded")
	assert.Contains(t, v.Reason, "Approving publishes")
	assert.NotContains(t, strings.ToLower(v.Reason), "say so")

	failed := judgePush(t, "fail", "")
	assert.Equal(t, "ask", failed.Decision)
	assert.Contains(t, failed.Reason, "failed")
}

// TestUngatedPushDeniesALeasedWorker pins that a bound session is never offered the prompt:
// approving it would publish from a boundary that does not own the branch.
func TestUngatedPushDeniesALeasedWorker(t *testing.T) {
	lease := narrowLease()
	v := judgePush(t, "", lease.ID, lease)
	assert.Equal(t, "deny", v.Decision)
	assert.Equal(t, string(denyRulePushUngated), v.Rule)
	assert.Contains(t, v.Reason, lease.ID)
	assert.Contains(t, v.Reason, "workers do not publish")
	assert.NotContains(t, strings.ToLower(v.Reason), "say so")
}

// TestGatedPushIsNeverAsked pins that the prompt appears only when consent is needed.
func TestGatedPushIsNeverAsked(t *testing.T) {
	v := judgePush(t, "pass", "")
	assert.Contains(t, []string{"pass", "advise"}, v.Decision)
}
