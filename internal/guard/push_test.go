package guard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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
		args += fmt.Sprintf(`"%s"`, a)
	}
	body := fmt.Sprintf(`{"ts":1,"inv":"i","kind":"started","command":{"arguments":[%s]},"magus_version":"%s"}`+"\n", args, version)
	if status != "" {
		body += fmt.Sprintf(`{"ts":2,"inv":"i","kind":"finished","status":"%s"}`+"\n", status)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
}

func TestGateCoverageReadsTheRunLog(t *testing.T) {
	t.Parallel()

	t.Run("a green gate at this commit covers the push", func(t *testing.T) {
		dir := t.TempDir()
		runLog(t, dir, "a.jsonl", "v0.4.3-97-gabc1234-dirty", "pass", "affected", "ci")
		assert.Equal(t, gatePassed, gateCoverageAt(dir, "abc1234"))
		decision, _ := gradePushWithoutGate(pushEvidence{cover: gateCoverageAt(dir, "abc1234"), commit: "abc1234"}, "")
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
		runLog(t, dir, "a.jsonl", "v0.4.3-97-gabc1234", "", "affected", "ci")
		assert.Equal(t, gateIncomplete, gateCoverageAt(dir, "abc1234"))
		_, reason := gradePushWithoutGate(pushEvidence{cover: gateCoverageAt(dir, "abc1234"), commit: "abc1234"}, "")
		assert.NotContains(t, reason, "RED")
	})

	t.Run("a green gate at a DIFFERENT commit is not coverage", func(t *testing.T) {
		// The case this rule exists for: gate green, commit, push. The work that moved
		// since is exactly what nothing has checked.
		dir := t.TempDir()
		runLog(t, dir, "a.jsonl", "v0.4.3-97-gdeadbee", "pass", "affected", "ci")
		assert.Equal(t, gateAbsent, gateCoverageAt(dir, "abc1234"))
		decision, _ := gradePushWithoutGate(pushEvidence{cover: gateCoverageAt(dir, "abc1234"), commit: "abc1234"}, "")
		assert.Equal(t, decisionDeny, decision)
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
			decision, reason := gradePushWithoutGate(pushEvidence{cover: cover, commit: "abc1234"}, lease)
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

	assert.True(t, namesCommit("v0.4.3-97-gabc1234-dirty", "abc1234"))
	assert.True(t, namesCommit("v0.4.3-97-gabc1234", "abc1234567"), "describe abbreviated shorter")
	assert.True(t, namesCommit("v0.4.3-97-gabc1234567", "abc1234"), "caller abbreviated shorter")
	assert.False(t, namesCommit("v0.4.3-97-gabc1234", "def5678"))
	assert.False(t, namesCommit("", "abc1234"))
	assert.False(t, namesCommit("v0.4.3-97-gabc1234", ""))
	assert.False(t, namesCommit("v0.4.3", "abc1234"), "a release build names no commit")
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
				Target: types.TargetCI, Scope: sessions.GateScopeAffected, Commit: rev.full, Outcome: sessions.OutcomePass,
			}, sessions.InvocationStart{Workspace: root}))
			assert.Equal(t, gatePassed, gateVerdictAt(root, rev.short))

			runs := t.TempDir()
			runLog(t, runs, "a.jsonl", "v0.4.3-1-g"+rev.full, "pass", "affected", "ci")
			assert.Equal(t, gatePassed, gateCoverageAt(runs, rev.short))
		})
	}
}

// The verdict reads through the store's cached gate fold, so it must still see a gate that
// finished after an earlier push cached the fold, and the newest verdict at a commit wins.
func TestGateVerdictSeesAGateRecordedAfterTheLastRead(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	const commit = "42a1c0cc84b21f0e9d8c7b6a5f4e3d2c1b0a9f8"
	require.NoError(t, sessions.RecordGate(dir, sessions.GateResult{
		Target: types.TargetCI, Scope: sessions.GateScopeAffected, Commit: commit, Outcome: sessions.OutcomeFail,
	}, sessions.InvocationStart{Workspace: root}))
	assert.Equal(t, gateFailed, gateVerdictAt(root, commit[:12]))

	// Records order by millisecond; a later one puts the pass after the fail.
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, sessions.RecordGate(dir, sessions.GateResult{
		Target: types.TargetCI, Scope: sessions.GateScopeAffected, Commit: commit, Outcome: sessions.OutcomePass,
	}, sessions.InvocationStart{Workspace: root}))
	assert.Equal(t, gatePassed, gateVerdictAt(root, commit[:12]))
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
			assert.False(t, namesCommit("v0.4.3-1-g42a1c0cc84b21f0e9d8c7b6a5f4e3d2c1b0a9f8", id))

			testkit.Isolate(t)
			root := t.TempDir()
			dir, err := sessions.Dir(root)
			require.NoError(t, err)
			require.NoError(t, sessions.RecordGate(dir, sessions.GateResult{
				Target: types.TargetCI, Scope: sessions.GateScopeAffected, Commit: "42a1c0cc84b21f0e9d8c7b6a5f4e3d2c1b0a9f8", Outcome: sessions.OutcomeFail,
			}, sessions.InvocationStart{Workspace: root}))
			assert.Equal(t, gateUnknown, gateVerdictAt(root, id))
		})
	}
}

// judgePush runs one push through Judge in a workspace whose run log exists, so a gate
// missing at HEAD is proven absent rather than unknown. gate, when set, records a finished
// `affected ci` run with that status at HEAD first.
func judgePush(t *testing.T, gate, lease string, leases ...types.Job) types.GuardJudgment {
	t.Helper()
	return judgePushAs(t, true, gate, lease, leases...)
}

// judgePushAs is judgePush for a caller that does or does not declare it renders ask.
func judgePushAs(t *testing.T, rendersAsk bool, gate, lease string, leases ...types.Job) types.GuardJudgment {
	t.Helper()
	ctx, _ := fleetFixture(t, leases...)
	runs := filepath.Join(hookLocation(ctx, Dependencies{}).cacheDir, cache.RunsDir)
	require.NoError(t, os.MkdirAll(runs, 0o755))
	if gate != "" {
		runLog(t, runs, "a.jsonl", "v0.4.3-97-gabc1234", gate, "affected", "ci")
	}
	deps := Dependencies{Revision: func(context.Context, string, string) string { return "abc1234" }}
	return Judge(ctx, deps, Request{Input: "git push origin HEAD", Lease: lease, RendersAsk: rendersAsk})
}

// TestUngatedPushDeniesWhetherOrNotTheCallerRendersAsk pins that no push is ever asked
// about: a caller that renders ask and one that does not get the same deny.
func TestUngatedPushDeniesWhetherOrNotTheCallerRendersAsk(t *testing.T) {
	for _, renders := range []bool{true, false} {
		v := judgePushAs(t, renders, "", "")
		assert.Equal(t, verdictWithRule(decisionDeny, string(denyRulePushUnverified)), unworded(v))
		assert.Contains(t, v.Reason, "abc1234", "the refusal names what it refused")
		assert.NotContains(t, v.Reason, "predates approval prompts")
	}

	gated := judgePushAs(t, false, "pass", "")
	assert.Contains(t, []string{decisionPass, decisionAdvise}, gated.Decision, "a covered push passes")
}

// TestUngatedPushDeniesTheOrchestrator pins that the root is refused with the gate named:
// pull requests went out with no local gate when the root was only asked.
func TestUngatedPushDeniesTheOrchestrator(t *testing.T) {
	v := judgePush(t, "", "")
	assert.Equal(t, verdictWithRule(decisionDeny, string(denyRulePushUnverified)), unworded(v))
	assert.Contains(t, v.Reason, "abc1234", "the refusal names the commit it publishes")
	assert.Contains(t, v.Reason, "no `ci` run is recorded")
	assert.NotContains(t, v.Reason, "Approving")

	_, full := gradePushWithoutGate(pushEvidence{cover: gateAbsent, commit: "abc1234"}, "")
	assert.Contains(t, full, "affected ci")

	failed := judgePush(t, "fail", "")
	assert.Equal(t, decisionDeny, failed.Decision)
	assert.Contains(t, failed.Reason, "failed")
}

// TestGhPrCreateMeetsThePushGate pins that opening a pull request is graded at the head
// commit like a push, and that the other gh verbs are not.
func TestGhPrCreateMeetsThePushGate(t *testing.T) {
	for _, line := range []string{"gh pr create --fill", "gh --repo x/y pr create -t t -b b"} {
		ctx, _ := fleetFixture(t)
		require.NoError(t, os.MkdirAll(filepath.Join(hookLocation(ctx, Dependencies{}).cacheDir, cache.RunsDir), 0o755))
		deps := Dependencies{Revision: func(context.Context, string, string) string { return "abc1234" }}
		v := Judge(ctx, deps, Request{Input: line, RendersAsk: true})
		assert.Equal(t, verdictWithRule(decisionDeny, string(denyRulePushUnverified)), unworded(v), line)
		assert.Contains(t, v.Reason, "abc1234", line)
	}
	for _, line := range []string{"gh pr view 12", "gh pr list"} {
		ctx, _ := fleetFixture(t)
		v := Judge(ctx, Dependencies{}, Request{Input: line, RendersAsk: true})
		assert.NotEqual(t, string(denyRulePushUnverified), v.Rule, line)
	}
}

// TestUngatedPushDenyStillAsksTheCommandRule pins the line that merged a pull request past
// the queue: the push verdict must not hide a `gh pr merge --admin` the workspace rule denies.
func TestUngatedPushDenyStillAsksTheCommandRule(t *testing.T) {
	ctx, _ := fleetFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(hookLocation(ctx, Dependencies{}).cacheDir, cache.RunsDir), 0o755))
	probe := &commandRuleProbe{answer: types.GuardVerdict{Decision: types.GuardDeny, Reason: "queue it"}}
	deps := Dependencies{
		Revision:    func(context.Context, string, string) string { return "abc1234" },
		CommandRule: probe.rule(),
	}
	line := "git push -q -u origin topic && gh pr merge topic --squash --admin"

	v := Judge(ctx, deps, Request{Input: line, RendersAsk: true})
	assert.Equal(t, decisionDeny, v.Decision, "the line is refused whichever rule speaks")

	probe.answer = types.GuardVerdict{Decision: types.GuardAdvise, Reason: "batch it"}
	v = Judge(ctx, deps, Request{Input: line, RendersAsk: true})
	assert.Equal(t, types.GuardJudgment{SchemaVersion: v.SchemaVersion, Decision: decisionDeny, Reason: v.Reason, Rule: string(denyRulePushUnverified)}, v, "an advise leaves the push deny as it stood")
	assert.NotContains(t, v.Reason, "batch it")
}

// TestUngatedPushDeniesALeasedWorker pins that a bound session is never offered the prompt:
// approving it would publish from a boundary that does not own the branch. lease-vcs
// refuses a worker's push before the push gate grades it, since pushing stays with the
// orchestrator whatever the gate record says, so the gate's own worker arm is pinned
// directly.
func TestUngatedPushDeniesALeasedWorker(t *testing.T) {
	lease := narrowLease()
	ctx, _ := fleetFixture(t, lease)
	v := Judge(ctx, Dependencies{Revision: func(context.Context, string, string) string { return "abc1234" }},
		Request{Input: "git push origin HEAD", Lease: lease.ID, RendersAsk: true})
	assert.Equal(t, verdictWithRule(decisionDeny, string(denyRuleLeaseVCS)), unworded(v))
	assert.NotContains(t, v.Reason, "\nsee: ", "the rule's page is in the stored verdict")
	full := storedVerdict(t, hookLocation(ctx, Dependencies{}).cacheDir, v.Reason)
	assert.Contains(t, full, lease.ID)
	assert.Contains(t, full, "every other version-control verb stays with the orchestrator")
	assert.NotContains(t, strings.ToLower(full), "say so")

	decision, reason := gradePushWithoutGate(pushEvidence{cover: gateAbsent, commit: "abc1234"}, lease.ID)
	assert.Equal(t, decisionDeny, decision)
	assert.Contains(t, reason, lease.ID)
	assert.Contains(t, reason, "workers do not publish")
}

// A root session holding a parentless row is the root under lease-vcs, so the push gate
// names the gate and not the orchestrator.
func TestUngatedPushDeniesARootSessionHoldingItsOwnLease(t *testing.T) {
	lease := narrowLease()
	lease.Parent = ""
	ctx, _ := fleetFixture(t, lease)
	runs := filepath.Join(hookLocation(ctx, Dependencies{}).cacheDir, cache.RunsDir)
	require.NoError(t, os.MkdirAll(runs, 0o755))
	deps := Dependencies{Revision: func(context.Context, string, string) string { return "abc1234" }}
	v := Judge(ctx, deps, Request{Input: "git push origin HEAD", Lease: lease.ID, RendersAsk: true, Host: "claude-code", Session: "root-session"})
	assert.Equal(t, verdictWithRule(decisionDeny, string(denyRulePushUnverified)), unworded(v))
	assert.NotContains(t, v.Reason, "workers do not publish")
}

// TestGatedPushIsNeverRefused pins that a covered push passes.
func TestGatedPushIsNeverRefused(t *testing.T) {
	v := judgePush(t, "pass", "")
	assert.Contains(t, []string{decisionPass, decisionAdvise}, v.Decision)
}

// TestLocatePushFollowsTheLine pins which directory a push runs in, and that a directory
// only knowable by running the line is not located at all.
func TestLocatePushFollowsTheLine(t *testing.T) {
	t.Parallel()

	here := pushSite{dir: "/w"}
	at := func(dir string) pushSite { return pushSite{dir: dir, relocated: true} }
	for command, want := range map[string]pushSite{
		"git push":                                  here,
		"git push origin HEAD:refs/heads/x":         here,
		"git push origin --delete topic":            here,
		`git push origin "$BR"`:                     here,
		"git push -u origin topic":                  {dir: "/w", rev: "topic"},
		"git push origin +main:main":                {dir: "/w", rev: "main"},
		"git -c core.x=y push origin topic":         {dir: "/w", rev: "topic"},
		"git -C /b push":                            at("/b"),
		"git -C ../b push origin HEAD:refs/heads/x": at("/b"),
		"git -C b -C c push":                        at("/w/b/c"),
		"git --no-pager -c x=y -C /b -P push":       at("/b"),
		`git -C "" push`:                            here,
		"git --namespace ns push origin topic":      {dir: "/w", rev: "topic"},
		"cd /b && git push":                         at("/b"),
		"cd /b; git push":                           at("/b"),
		"cd b && git -C c push":                     at("/w/b/c"),
		`cd "$T" && git -C /b push`:                 at("/b"),
		"(cd /b && make); git push":                 here,
		"(cd /b && git push)":                       at("/b"),
		"bash -c 'git push'":                        here,
		"> out; git push":                           here,
		"hg -R /b push":                             at("/b"),
		"jj git push -R /b":                         at("/b"),
	} {
		got, ok := locatePush(command, DialectBash, "/w")
		assert.True(t, ok, command)
		assert.Equal(t, want, got, command)
	}
	for _, command := range []string{
		`git -C "$T" push`,
		`git -C "$T/x" push`,
		`cd "$T" && git push`,
		"cd ~/b && git push",
		"cd /b || git push",
		"bash -c 'git -C /b push'",
		"git --git-dir=/b/.git push",
		"git --work-tree /b push",
		"git -C /b --git-dir .git push",
		"git -c alias.p=push p",
		"git --help push",
		"if true; then git push; fi",
		"echo git push",
		"git status",
	} {
		_, ok := locatePush(command, DialectBash, "/w")
		assert.False(t, ok, command)
	}
}

// TestLocateCallsUnrollsALiteralLoop pins that each word of a literal for loop yields its
// own located push, with the loop variable read as that word and a cd carried into the
// next iteration.
func TestLocateCallsUnrollsALiteralLoop(t *testing.T) {
	t.Parallel()

	sites := func(command string) []pushSite {
		t.Helper()
		calls, ok := locateCalls(command, DialectBash, "/w", isPush, false)
		require.True(t, ok, command)
		var out []pushSite
		for _, c := range calls {
			require.False(t, c.unfollowed, command)
			site, ok := parsePush(c.inv.Name, c.args, c.at)
			require.True(t, ok, command)
			out = append(out, site)
		}
		return out
	}
	assert.Equal(t, []pushSite{
		{dir: "/b", relocated: true, rev: "topic-b"},
		{dir: "/c", relocated: true, rev: "topic-c"},
	}, sites(`for d in b 'c'; do git -C /$d push origin "topic-${d}"; done`))
	assert.Equal(t, []pushSite{
		{dir: "/w/b", relocated: true, rev: "b"},
		{dir: "/w/b/c", relocated: true, rev: "c"},
	}, sites("for d in b c; do cd $d && git push origin $d; done"))
	assert.Equal(t, []pushSite{
		{dir: "/w", rev: "x"}, {dir: "/w", rev: "y"}, {dir: "/w", rev: "z"},
	}, sites("for a in x y; do git push origin $a; done; for a in z; do git push origin $a; done"))

	first, ok := locatePush("for d in /b /c; do git -C $d push; done", DialectBash, "/w")
	assert.True(t, ok)
	assert.Equal(t, pushSite{dir: "/b", relocated: true}, first)

	for _, command := range []string{
		"for d in $(ls); do git -C /$d push; done",
		"for d in /b*; do git -C $d push; done",
		"for d in /b /c; do git -C $d push; continue; done",
		"for d in /b; do read d; git -C $d push; done",
		"for ((i=0; i<2; i++)); do git push; done",
		"select d in /b; do git -C $d push; done",
		"g() { :; }; for d in /b; do git -C $d push; done",
		"for d in /b; do if true; then git -C $d push; fi; done",
	} {
		_, ok := locatePush(command, DialectBash, "/w")
		assert.False(t, ok, command)
	}
}

// judgeRelocatedPush judges command, built from checkout B's path, in checkout A. A's HEAD
// is abc1234 and B's is def5678; each holds a run log, with a green gate at its HEAD when
// gated says so.
func judgeRelocatedPush(t *testing.T, gatedA, gatedB bool, command func(b string) string) types.GuardJudgment {
	t.Helper()
	ctx, _ := fleetFixture(t)
	runsA := filepath.Join(hookLocation(ctx, Dependencies{}).cacheDir, cache.RunsDir)
	b, cacheB := t.TempDir(), t.TempDir()
	runsB := filepath.Join(cacheB, cache.RunsDir)
	require.NoError(t, os.WriteFile(filepath.Join(b, "magus.yaml"), nil, 0o644))
	for runs, gate := range map[string]struct {
		on     bool
		commit string
	}{runsA: {gatedA, "abc1234"}, runsB: {gatedB, "def5678"}} {
		require.NoError(t, os.MkdirAll(runs, 0o755))
		if gate.on {
			runLog(t, runs, "a.jsonl", "v0.4.3-97-g"+gate.commit, "pass", "affected", "ci")
		}
	}
	deps := Dependencies{
		CacheDir: func(root string) (string, error) {
			if root != b {
				return "", os.ErrNotExist
			}
			return cacheB, nil
		},
		Revision: func(_ context.Context, dir, _ string) string {
			if dir == b {
				return "def5678"
			}
			return "abc1234"
		},
	}
	return Judge(ctx, deps, Request{Input: command(b), RendersAsk: true})
}

// TestRelocatedPushGradesTheCheckoutItRunsIn pins that a push another directory runs is
// graded by THAT checkout's revision and gate record, not the hook's.
func TestRelocatedPushGradesTheCheckoutItRunsIn(t *testing.T) {
	dashC := func(b string) string { return fmt.Sprintf("git -C %s push origin HEAD:refs/heads/x", b) }

	t.Run("an ungated checkout is refused, though the hook's own is gated", func(t *testing.T) {
		v := judgeRelocatedPush(t, true, false, dashC)
		assert.Equal(t, verdictWithRule(decisionDeny, string(denyRulePushUnverified)), unworded(v))
		assert.Contains(t, v.Reason, "def5678")
		assert.NotContains(t, v.Reason, "abc1234")
	})

	t.Run("a gated checkout passes, though the hook's own is not", func(t *testing.T) {
		v := judgeRelocatedPush(t, false, true, dashC)
		assert.Contains(t, []string{decisionPass, decisionAdvise}, v.Decision)
	})

	t.Run("a leading cd relocates it the same way", func(t *testing.T) {
		v := judgeRelocatedPush(t, true, false, func(b string) string { return fmt.Sprintf("cd %s && git push", b) })
		assert.Equal(t, decisionDeny, v.Decision)
		assert.Contains(t, v.Reason, "def5678")
	})

	t.Run("a plain push still grades the hook's checkout", func(t *testing.T) {
		v := judgeRelocatedPush(t, false, true, func(string) string { return "git push" })
		assert.Equal(t, decisionDeny, v.Decision)
		assert.Contains(t, v.Reason, "abc1234")
	})

	t.Run("global options around -C still grade the checkout it names", func(t *testing.T) {
		v := judgeRelocatedPush(t, true, false, func(b string) string {
			return fmt.Sprintf("git --no-pager -c color.ui=never -C %s -P push origin HEAD:refs/heads/x", b)
		})
		assert.Equal(t, verdictWithRule(decisionDeny, string(denyRulePushUnverified)), unworded(v))
		assert.Contains(t, v.Reason, "def5678")
	})

	t.Run("a pager option does not hide a push from the hook's checkout", func(t *testing.T) {
		v := judgeRelocatedPush(t, false, true, func(string) string { return "git -P push" })
		assert.Equal(t, verdictWithRule(decisionDeny, string(denyRulePushUnverified)), unworded(v))
		assert.Contains(t, v.Reason, "abc1234")
	})

	t.Run("a directory only the shell knows keeps the advisory", func(t *testing.T) {
		v := judgeRelocatedPush(t, false, false, func(string) string { return `git -C "$T" push` })
		assert.Equal(t, verdictWithRule(decisionAdvise, string(advisoryPushGate)), unworded(v))
	})
}

// TestPushAfterACommitOnTheLineIsRefused pins the bypass: every push alone on its line was
// graded and every `commit && push` passed, because the revision is read before the line
// runs. A green gate at that HEAD covers none of what the commit adds.
func TestPushAfterACommitOnTheLineIsRefused(t *testing.T) {
	deps := Dependencies{Revision: func(context.Context, string, string) string { return "abc1234" }}
	// A fresh session per line: a repeated denial is worded as a one-line summary, and
	// each line must show the instruction the first time it is refused.
	gated := func(t *testing.T) context.Context {
		ctx, _ := fleetFixture(t)
		runs := filepath.Join(hookLocation(ctx, Dependencies{}).cacheDir, cache.RunsDir)
		require.NoError(t, os.MkdirAll(runs, 0o755))
		runLog(t, runs, "a.jsonl", "v0.4.3-97-gabc1234", "pass", "affected", "ci")
		return ctx
	}

	refused := []string{
		"git commit -m x && git push",
		"git add x.go && git commit -m x ; git push origin HEAD",
		"git commit -m x || git push",
		"git commit -m x\ngit push",
		"(git commit -m x && git push)",
		"git -C . commit -m x && git push",
		"git merge topic && git push",
		"git cherry-pick abc && git push",
		"git am p.patch && git push",
		"git rebase main && git push",
		"git reset --soft HEAD~1 && git push",
		"hg commit -m x && hg push",
		"sl ci -m x && sl push",
		"jj commit -m x && jj git push",
		"jj describe -m x && jj git push",
		"git push && git commit -m x && git push",
	}
	for _, line := range refused {
		v := Judge(gated(t), deps, Request{Input: line, RendersAsk: true})
		assert.Equal(t, verdictWithRule(decisionDeny, string(denyRulePushUnverified)), unworded(v), line)
		assert.Contains(t, v.Reason, "in a separate call", line)
	}

	passes := []string{
		"git push origin HEAD",
		"git push && git commit -m x",
		"git status && git push",
		"hg push",
	}
	ctx := gated(t)
	for _, line := range passes {
		v := Judge(ctx, deps, Request{Input: line, RendersAsk: true})
		assert.Contains(t, []string{decisionPass, decisionAdvise}, v.Decision, line)
	}
}

// TestPushAfterACommitStandsDownWithNoRunLog pins that the refusal needs a gate record to
// stand against: a fresh clone has nothing to prove, so the line passes as it did.
func TestPushAfterACommitStandsDownWithNoRunLog(t *testing.T) {
	ctx, _ := fleetFixture(t)
	deps := Dependencies{Revision: func(context.Context, string, string) string { return "abc1234" }}
	v := Judge(ctx, deps, Request{Input: "git commit -m x && git push", RendersAsk: true})
	assert.Contains(t, []string{decisionPass, decisionAdvise}, v.Decision)
}

func replayedBranch(t *testing.T) (ctx context.Context, repo *testkit.Repo, gated, replayed string) {
	t.Helper()
	testkit.Isolate(t)
	repo = testkit.NewRepository(t, map[string]string{".gitignore": ".magus/\n", "a.go": "package a\n"})
	cacheDir := filepath.Join(repo.Root(), ".magus")
	require.NoError(t, os.MkdirAll(filepath.Join(cacheDir, cache.RunsDir), 0o755))
	dir, err := sessions.Dir(repo.Root())
	require.NoError(t, err)
	gated = repo.Head()
	require.NoError(t, sessions.RecordGate(dir, sessions.GateResult{
		Target: types.TargetCI, Scope: sessions.GateScopeAffected, Ref: "main", Commit: gated, Outcome: sessions.OutcomePass, Inv: "inv-green",
	}, sessions.InvocationStart{Workspace: repo.Root()}))
	fixtureGit(t, repo.Root(), "commit", "-q", "--amend", "-m", "reworded")
	replayed = fixtureGit(t, repo.Root(), "rev-parse", "HEAD")
	require.NotEqual(t, gated, replayed)
	return context.WithValue(t.Context(), locationKey{}, location{cacheDir: cacheDir, workspace: repo.Root()}), repo, gated, replayed
}

func coverageAtPush(ctx context.Context, repo *testkit.Repo, commit string) gateCoverage {
	deps := Dependencies{Revision: func(context.Context, string, string) string { return commit[:12] }}
	return pushCoverage(ctx, deps, location{cacheDir: filepath.Join(repo.Root(), ".magus"), workspace: repo.Root()}, "git push", DialectBash, repo.Root()).cover
}

func TestPushGateAcceptsAPassingGateOnTheIdenticalTree(t *testing.T) {
	ctx, repo, gated, replayed := replayedBranch(t)

	lineage := gateLineageAt(ctx, types.VCSOptions{}, repo.Root(), replayed, readGateHistory(repo.Root()))
	rec, ok := lineage.replay, lineage.replayed
	require.True(t, ok)
	assert.Equal(t, sessions.GateRecord{
		GateResult: sessions.GateResult{Target: types.TargetCI, Scope: sessions.GateScopeAffected, Ref: "main", Commit: gated, Outcome: sessions.OutcomePass, Inv: "inv-green"},
		At:         rec.At,
	}, rec)

	assert.Equal(t, gatePassed, coverageAtPush(ctx, repo, replayed))
}

func TestPushCoverageNamesTheReplayedRun(t *testing.T) {
	ctx, repo, _, replayed := replayedBranch(t)
	deps := Dependencies{Revision: func(context.Context, string, string) string { return replayed[:12] }}
	at := location{cacheDir: filepath.Join(repo.Root(), ".magus"), workspace: repo.Root()}

	ev := pushCoverage(ctx, deps, at, "git push", DialectBash, repo.Root())
	assert.Equal(t, gatePassed, ev.cover)
	assert.Equal(t, "inv-green", ev.replay.Inv)

	v := Judge(ctx, deps, Request{Input: "git push", RendersAsk: true})
	assert.Contains(t, v.Context, "run inv-green")
}

func TestPushGateStillNeedsItsOwnGateForADifferentTree(t *testing.T) {
	ctx, repo, _, _ := replayedBranch(t)
	repo.Commit("more", map[string]string{"b.go": "package a\n"})
	changed := fixtureGit(t, repo.Root(), "rev-parse", "HEAD")

	ok := gateLineageAt(ctx, types.VCSOptions{}, repo.Root(), changed, readGateHistory(repo.Root())).replayed
	assert.False(t, ok)
	assert.Equal(t, gateAbsent, coverageAtPush(ctx, repo, changed))
}

// GateScopeSuite pins running-doc entry 277: a passing `magus run ci libs/cmdline` made
// the branch read as gated, so the guard refused `magus affected ci` as a repeat and would
// have let the push out, while the last `affected ci` had failed.
type GateScopeSuite struct {
	suite.Suite
}

func TestGateScopeSuite(t *testing.T) { suite.Run(t, new(GateScopeSuite)) }

func (s *GateScopeSuite) recordAt(root string, g sessions.GateResult) {
	dir, err := sessions.Dir(root)
	s.Require().NoError(err)
	s.Require().NoError(sessions.RecordGate(dir, g, sessions.InvocationStart{Workspace: root}))
	time.Sleep(2 * time.Millisecond)
}

func (s *GateScopeSuite) projectPass(commit string) sessions.GateResult {
	return sessions.GateResult{
		Target: types.TargetCI, Scope: sessions.GateScopeProject, Ref: "main", Commit: commit,
		Outcome: sessions.OutcomePass, Projects: []string{"libs/cmdline"}, Inv: "inv-cmdline",
	}
}

func (s *GateScopeSuite) TestAProjectPassAfterARedAffectedGateLeavesTheBranchRed() {
	ctx, repo := verifyRepeatRepository(s.T(), 2, sessions.OutcomeFail)
	s.recordAt(repo.Root(), s.projectPass(repo.Head()))

	s.Empty(denyRepeatVerify(ctx, Dependencies{}, "", "./magus affected ci"), "the affected gate is not a repeat of a green one")
	s.Equal(gateFailed, coverageAtPush(ctx, repo, repo.Head()), "and the push is graded on the red affected verdict")
}

func (s *GateScopeSuite) TestAProjectPassAloneCoversNoPush() {
	ctx, repo := verifyRepeatRepository(s.T(), 0, sessions.OutcomeFail)
	other := repo.Commit("more", map[string]string{"b.go": "package a\n"})
	s.recordAt(repo.Root(), s.projectPass(other))

	s.Equal(gateProjectScoped, coverageAtPush(ctx, repo, other))
	decision, reason := gradePushWithoutGate(pushEvidence{cover: gateProjectScoped, commit: other[:12]}, "")
	s.Equal(decisionDeny, decision)
	s.Contains(reason, "covered named projects, not every project the branch affects")
}

func (s *GateScopeSuite) TestAProjectPassIsNoReplayOfTheTree() {
	ctx, repo := verifyRepeatRepository(s.T(), 0, sessions.OutcomeFail)
	gated := repo.Commit("more", map[string]string{"b.go": "package a\n"})
	s.recordAt(repo.Root(), s.projectPass(gated))
	fixtureGit(s.T(), repo.Root(), "commit", "-q", "--amend", "-m", "reworded")
	replayed := fixtureGit(s.T(), repo.Root(), "rev-parse", "HEAD")

	ok := gateLineageAt(ctx, types.VCSOptions{}, repo.Root(), replayed, readGateHistory(repo.Root())).replayed
	s.False(ok)
}

func (s *GateScopeSuite) TestAPreScopeVerdictSaysItReadsAsProjectScope() {
	testkit.Isolate(s.T())
	root := s.T().TempDir()
	dir, err := sessions.Dir(root)
	s.Require().NoError(err)
	w, err := sessions.Open(dir, sessions.NewID(), sessions.InvocationStart{Workspace: root})
	s.Require().NoError(err)
	const commit = "42a1c0cc84b21f0e9d8c7b6a5f4e3d2c1b0a9f8"
	s.Require().NoError(w.Append(sessions.KindGateResult, map[string]string{"target": "ci", "commit": commit, "outcome": sessions.OutcomePass}))

	s.Equal(gateUnscoped, gateVerdictAt(root, commit[:12]))
	decision, reason := gradePushWithoutGate(pushEvidence{cover: gateUnscoped, commit: commit[:12]}, "")
	s.Equal(decisionDeny, decision)
	s.Contains(reason, "recorded before gate records named their scope, so it reads as project scope")
}

func (s *GateScopeSuite) TestARunLogOfAProjectGateCoversNoPush() {
	const version = "v0.4.3-1-g42a1c0cc84b21f0e9d8c7b6a5f4e3d2c1b0a9f8"
	runs := s.T().TempDir()
	runLog(s.T(), runs, "a.jsonl", version, "pass", "run", "ci", "libs/cmdline")
	s.Equal(gateProjectScoped, gateCoverageAt(runs, "42a1c0cc84b2"))

	runLog(s.T(), runs, "b.jsonl", version, "pass", "affected", "ci")
	s.Equal(gatePassed, gateCoverageAt(runs, "42a1c0cc84b2"))
}

// NarrowPushSuite pins the push gate admitting narrow runs in place of a
// local `affected ci`: a branch gated at its first commit, then changed.
type NarrowPushSuite struct {
	suite.Suite
	repo  *testkit.Repo
	at    location
	gated string
	deps  Dependencies
}

func TestNarrowPushSuite(t *testing.T) { suite.Run(t, new(NarrowPushSuite)) }

func (s *NarrowPushSuite) SetupTest() {
	testkit.Isolate(s.T())
	s.repo = testkit.NewRepository(s.T(), map[string]string{".gitignore": ".magus/\n", "a.go": "package a\n"})
	s.gated = s.repo.Head()
	s.at = location{cacheDir: filepath.Join(s.repo.Root(), ".magus"), workspace: s.repo.Root()}
	s.Require().NoError(os.MkdirAll(filepath.Join(s.at.cacheDir, cache.RunsDir), 0o755))
	project := &types.Project{
		Path: ".", Sources: []types.Glob{{Pattern: "**/*.go"}}, MagusfileTargets: []string{"test", "generate"},
		Outputs:       []types.Glob{{Pattern: "gen/*.jsonl"}},
		TargetOutputs: map[string][]types.OutputRef{"generate": {{Glob: "gen/*.jsonl"}}},
	}
	ws := typestest.NewMockWorkspaceRepository(s.T())
	ws.EXPECT().All().Return([]*types.Project{project}).Maybe()
	ws.EXPECT().Get(".").Return(project).Maybe()
	ws.EXPECT().Get(mock.Anything).Return(nil).Maybe()
	s.deps = Dependencies{
		Inspect: func(context.Context, string) (types.WorkspaceRepository, error) { return ws, nil },
		VCS:     types.VCSOptions{BaseRef: s.gated},
	}
}

func (s *NarrowPushSuite) gateAtFirstCommit() {
	dir, err := sessions.Dir(s.repo.Root())
	s.Require().NoError(err)
	s.Require().NoError(sessions.RecordGate(dir, sessions.GateResult{
		Target: types.TargetCI, Scope: sessions.GateScopeAffected, Ref: "main", Commit: s.gated, Outcome: sessions.OutcomePass, Inv: "inv-green",
	}, sessions.InvocationStart{Workspace: s.repo.Root()}))
}

// passed records a passing run of target at revision, clean, with args.
func (s *NarrowPushSuite) passed(key, target, revision string, args ...string) {
	_, err := cache.NewOutputStore(s.at.cacheDir).Persist(s.T().Context(), key, []byte("ok\n"), cache.OutputDescriptor{
		Project: ".", Target: target, Revision: revision, ExtraArgs: args, TimestampMs: time.Now().UnixMilli(),
	})
	s.Require().NoError(err)
}

func (s *NarrowPushSuite) push(commit string) pushEvidence {
	s.deps.Revision = func(context.Context, string, string) string { return commit[:12] }
	return pushCoverage(s.T().Context(), s.deps, s.at, "git push", DialectBash, s.repo.Root())
}

func (s *NarrowPushSuite) TestNarrowRunsCoveringEveryChangedFileAdmitThePush() {
	s.gateAtFirstCommit()
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n", "b.go": "package a\n"})
	s.passed(strings.Repeat("a", 64), "go-test", head, "-run", "Push")

	ev := s.push(head)
	s.Equal(gateNarrow, ev.cover)
	decision, _ := gradePushWithoutGate(ev, "")
	s.Empty(decision)
	s.Contains(ev.admitted(), "goes out on narrow runs")
	s.Contains(ev.admitted(), "each of the 2 files this host builds that changed since "+s.gated[:12])
}

// gitCalls puts a git on PATH that logs each argv, one per line, before
// running the real one.
func (s *NarrowPushSuite) gitCalls() func() []string {
	if runtime.GOOS == "windows" {
		s.T().Skip("the logging git is a shell script")
	}
	real, err := exec.LookPath("git")
	s.Require().NoError(err)
	bin := s.T().TempDir()
	calls := filepath.Join(bin, "calls")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\nexec %q \"$@\"\n", calls, real)
	s.Require().NoError(os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755))
	s.T().Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		b, err := os.ReadFile(calls)
		if os.IsNotExist(err) {
			return nil
		}
		s.Require().NoError(err)
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

// Every gate used to cost a store fold and two git processes, which lapsed
// the guard's deadline on a busy store.
func (s *NarrowPushSuite) TestAPushJudgmentReadsTheStoreOnceAndGitOnceForEveryGate() {
	dir, err := sessions.Dir(s.repo.Root())
	s.Require().NoError(err)
	var gated []string
	for i := range 4 {
		c := s.repo.Commit(fmt.Sprintf("gated %d", i), map[string]string{"a.go": fmt.Sprintf("package a\n\nvar A = %d\n", i)})
		s.Require().NoError(sessions.RecordGate(dir, sessions.GateResult{
			Target: types.TargetCI, Scope: sessions.GateScopeAffected, Ref: "main", Commit: c, Outcome: sessions.OutcomePass, Inv: fmt.Sprintf("inv-%d", i),
		}, sessions.InvocationStart{Workspace: s.repo.Root()}))
		gated = append(gated, c)
		time.Sleep(2 * time.Millisecond)
	}
	head := s.repo.Commit("change", map[string]string{"b.go": "package a\n"})
	s.passed(strings.Repeat("d", 64), "go-test", head)

	reads := 0
	defer func(read func(string) (sessions.Fold, error)) { readGateFold = read }(readGateFold)
	readGateFold = func(dir string) (sessions.Fold, error) {
		reads++
		return sessions.ReadGateResults(dir)
	}
	calls := s.gitCalls()

	ev := s.push(head)
	s.Equal(gateNarrow, ev.cover)
	s.Contains(ev.admitted(), "changed since "+gated[3][:12], "the newest gate is the ancestor")
	s.Equal(1, reads)
	var walks []string
	for _, call := range calls() {
		if strings.Contains(call, "rev-list --ignore-missing --stdin") {
			walks = append(walks, call)
		}
		for _, c := range gated[:3] {
			s.NotContains(call, c[:12], "no git process names a gate the push does not descend from")
		}
	}
	s.Len(walks, 1)
}

func (s *NarrowPushSuite) TestAnUncoveredFileIsRefusedByName() {
	s.gateAtFirstCommit()
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n", "b.go": "package a\n"})
	s.passed(strings.Repeat("b", 64), "lint", head, "a.go")

	ev := s.push(head)
	s.Equal(gateAbsent, ev.cover)
	decision, reason := gradePushWithoutGate(ev, "")
	s.Equal(decisionDeny, decision)
	s.Contains(reason, "No passing run covers 1 of the 2 files changed since "+s.gated[:12])
	s.Contains(reason, "  b.go: run `")
	s.Contains(reason, "lint . -- b.go`")
	s.NotContains(reason, "  a.go:")
}

func (s *NarrowPushSuite) TestNoGateAndNoRunsIsRefused() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})

	ev := s.push(head)
	s.Equal(gateAbsent, ev.cover)
	decision, reason := gradePushWithoutGate(ev, "")
	s.Equal(decisionDeny, decision)
	s.Contains(reason, fmt.Sprintf("the merge base with %s, since no ancestor has a passing `ci`", s.gated))
	s.Contains(reason, "  a.go: run `")
	s.Contains(reason, "run test .`")
}

func (s *NarrowPushSuite) TestARunOfAnOlderFileCoversNothing() {
	s.gateAtFirstCommit()
	first := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.passed(strings.Repeat("c", 64), "go-test", first)
	head := s.repo.Commit("again", map[string]string{"a.go": "package a\n\nvar A = 2\n"})

	s.Equal(gateAbsent, s.push(head).cover)
}

func (s *NarrowPushSuite) TestAFileThisHostDoesNotBuildIsLeftToCI() {
	s.gateAtFirstCommit()
	head := s.repo.Commit("change", map[string]string{"pressure_plan9_test.go": "package a\n"})

	ev := s.push(head)
	s.Equal(gateNarrow, ev.cover)
	s.Contains(ev.admitted(), "pressure_plan9_test.go: built only on plan9; left to CI.")
}

func (s *NarrowPushSuite) TestAFileThisHostBuildsStillNeedsARun() {
	s.gateAtFirstCommit()
	head := s.repo.Commit("change", map[string]string{"pressure_plan9_test.go": "package a\n", "a.go": "package a\n\nvar A = 1\n"})

	ev := s.push(head)
	s.Equal(gateAbsent, ev.cover)
	_, reason := gradePushWithoutGate(ev, "")
	s.Contains(reason, "  a.go: run `")
	s.Contains(reason, "  pressure_plan9_test.go: built only on plan9; left to CI")
}

func (s *NarrowPushSuite) producedRun(key, target, revision string, dirty bool, finished time.Time) {
	_, err := cache.NewOutputStore(s.at.cacheDir).Persist(s.T().Context(), key, []byte("ok\n"), cache.OutputDescriptor{
		Project: ".", Target: target, Revision: revision, Dirty: dirty, TimestampMs: finished.UnixMilli(),
	})
	s.Require().NoError(err)
}

func (s *NarrowPushSuite) TestAGeneratedOutputIsCoveredByTheRunThatWroteIt() {
	s.gateAtFirstCommit()
	head := s.repo.Commit("regenerate", map[string]string{"gen/out.jsonl": "{}\n"})
	s.producedRun(strings.Repeat("d", 64), "generate", head, false, time.Now())

	s.Equal(gateNarrow, s.push(head).cover)
}

func (s *NarrowPushSuite) TestACleanRunOfATargetWithoutDeclaredOutputsCoversNoOutput() {
	s.gateAtFirstCommit()
	head := s.repo.Commit("regenerate", map[string]string{"gen/out.jsonl": "{}\n"})
	s.producedRun(strings.Repeat("8", 64), "test", head, false, time.Now())

	s.Equal(gateAbsent, s.push(head).cover)
}

func (s *NarrowPushSuite) TestAGeneratedOutputIsUncoveredWithoutAProducingRun() {
	s.gateAtFirstCommit()
	head := s.repo.Commit("regenerate", map[string]string{"gen/out.jsonl": "{}\n"})

	s.Equal(gateAbsent, s.push(head).cover)
}

func (s *NarrowPushSuite) TestAProducingRunOfAnOlderOutputCoversNothing() {
	s.gateAtFirstCommit()
	first := s.repo.Commit("regenerate", map[string]string{"gen/out.jsonl": "{}\n"})
	s.producedRun(strings.Repeat("e", 64), "generate", first, false, time.Now())
	head := s.repo.Commit("regenerate again", map[string]string{"gen/out.jsonl": "{\"a\":1}\n"})

	s.Equal(gateAbsent, s.push(head).cover)
}

func (s *NarrowPushSuite) TestADirtyProducingRunCoversAnOutputNotModifiedAfterIt() {
	s.gateAtFirstCommit()
	head := s.repo.Commit("regenerate", map[string]string{"gen/out.jsonl": "{}\n"})
	s.producedRun(strings.Repeat("f", 64), "generate", head, true, time.Now().Add(time.Minute))

	s.Equal(gateNarrow, s.push(head).cover)
}

func (s *NarrowPushSuite) TestADirtyProducingRunBeforeTheOutputChangedCoversNothing() {
	s.gateAtFirstCommit()
	head := s.repo.Commit("regenerate", map[string]string{"gen/out.jsonl": "{}\n"})
	s.producedRun(strings.Repeat("9", 64), "generate", head, true, time.Now().Add(-time.Minute))

	s.Equal(gateAbsent, s.push(head).cover)
}

func (s *NarrowPushSuite) TestARefusalNamesTheProducingTargetForAnOutput() {
	s.gateAtFirstCommit()
	head := s.repo.Commit("regenerate", map[string]string{"gen/out.jsonl": "{}\n"})

	_, reason := gradePushWithoutGate(s.push(head), "")
	s.Contains(reason, "  gen/out.jsonl: run `")
	s.Contains(reason, "run generate .`")
}

func (s *NarrowPushSuite) TestTheWaiverStillAdmits() {
	const waived = "abc1234def567890abc1234def567890abc12345"
	v := waivedPush(s.T(), "abc1234", func(ctx context.Context, store *job.Store) {
		_, err := WaiveGate(ctx, store, waived, "CI gates the release branch")
		s.Require().NoError(err)
	})
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
	s.Contains(v.Context, "abc1234 goes out ungated under the waiver")
}

// UnrunGateSuite pins a gate that could not run a target here. A stopped
// docker daemon on 11-05 recorded a failed gate, and no narrow run could clear
// the push.
type UnrunGateSuite struct {
	suite.Suite
	root, dir string
}

const unrunCommit = "42a1c0cc84b21f0e9d8c7b6a5f4e3d2c1b0a9f8"

func TestUnrunGateSuite(t *testing.T) {
	suite.Run(t, new(UnrunGateSuite))
}

func (s *UnrunGateSuite) SetupTest() {
	testkit.Isolate(s.T())
	s.root = s.T().TempDir()
	dir, err := sessions.Dir(s.root)
	s.Require().NoError(err)
	s.dir = dir
}

func (s *UnrunGateSuite) record(outcome string, scope sessions.GateScope) {
	s.Require().NoError(sessions.RecordGate(s.dir, sessions.GateResult{
		Target: types.TargetCI, Scope: scope, Commit: unrunCommit, Outcome: outcome,
		Unrun: []string{"images/magus:image-build: MGS3004 docker is installed but not ready"},
	}, sessions.InvocationStart{Workspace: s.root}))
	// Records order by millisecond.
	time.Sleep(2 * time.Millisecond)
}

func (s *UnrunGateSuite) TestAPushTheGateCouldNotFullyRunPassesNamingWhatIsOwed() {
	s.record(sessions.OutcomeUnrun, sessions.GateScopeAffected)
	cover, rec := readGateHistory(s.root).recordAt(unrunCommit[:12])
	s.Require().Equal(gateUnrun, cover)

	ev := pushEvidence{cover: cover, commit: unrunCommit[:12], unrun: rec}
	decision, _ := gradePushWithoutGate(ev, "")
	s.Empty(decision)
	s.Contains(ev.admitted(), "could not run here:\n  images/magus:image-build: MGS3004")
}

func (s *UnrunGateSuite) TestAFailedGateAtTheCommitOutranksALaterUnrunOne() {
	s.record(sessions.OutcomeFail, sessions.GateScopeAffected)
	s.record(sessions.OutcomeUnrun, sessions.GateScopeAffected)
	s.Equal(gateFailed, gateVerdictAt(s.root, unrunCommit[:12]))
}

func (s *UnrunGateSuite) TestAnUnrunGateOverNamedProjectsIsProjectScope() {
	s.record(sessions.OutcomeUnrun, sessions.GateScopeProject)
	s.Equal(gateProjectScoped, gateVerdictAt(s.root, unrunCommit[:12]))
}

func (s *UnrunGateSuite) TestAnUnrunBranchGateOutranksAProjectPass() {
	s.record(sessions.OutcomeUnrun, sessions.GateScopeAffected)
	s.record(sessions.OutcomePass, sessions.GateScopeProject)
	s.Equal(gateUnrun, gateVerdictAt(s.root, unrunCommit[:12]))
}

// RemoteGatePushSuite pins the push gate under ci.remote_gate.
type RemoteGatePushSuite struct {
	suite.Suite
	repo *testkit.Repo
	at   location
	deps Dependencies
}

func TestPushGateRemoteSuite(t *testing.T) { suite.Run(t, new(RemoteGatePushSuite)) }

func (s *RemoteGatePushSuite) SetupTest() {
	testkit.Isolate(s.T())
	s.repo = testkit.NewRepository(s.T(), map[string]string{".gitignore": ".magus/\n", "a.go": "package a\n"})
	s.at = location{cacheDir: filepath.Join(s.repo.Root(), ".magus"), workspace: s.repo.Root()}
	s.Require().NoError(os.MkdirAll(filepath.Join(s.at.cacheDir, cache.RunsDir), 0o755))
}

func (s *RemoteGatePushSuite) declare() {
	s.Require().NoError(os.WriteFile(filepath.Join(s.repo.Root(), "magus.yaml"), []byte("ci:\n  remote_gate: github-actions\n"), 0o644))
}

func (s *RemoteGatePushSuite) push(commit string) pushEvidence {
	s.deps.Revision = func(context.Context, string, string) string { return commit[:12] }
	return pushCoverage(s.T().Context(), s.deps, s.at, "git push", DialectBash, s.repo.Root())
}

func (s *RemoteGatePushSuite) TestPushGateAdmitsARemoteGate() {
	s.declare()
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})

	ev := s.push(head)
	s.Equal(gateRemote, ev.cover)
	decision, _ := gradePushWithoutGate(ev, "")
	s.Empty(decision)
	s.Contains(ev.admitted(), "no local `ci` at "+head[:12])
	s.Contains(ev.admitted(), "github-actions runs it on the pull request (ci.remote_gate)")
}

func (s *RemoteGatePushSuite) TestPushGateStillRefusesAFailedGateUnderARemoteGate() {
	s.declare()
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	dir, err := sessions.Dir(s.repo.Root())
	s.Require().NoError(err)
	s.Require().NoError(sessions.RecordGate(dir, sessions.GateResult{
		Target: types.TargetCI, Scope: sessions.GateScopeAffected, Ref: "main", Commit: head, Outcome: sessions.OutcomeFail, Inv: "inv-red",
	}, sessions.InvocationStart{Workspace: s.repo.Root()}))

	ev := s.push(head)
	s.Equal(gateFailed, ev.cover)
	decision, _ := gradePushWithoutGate(ev, "")
	s.Equal(decisionDeny, decision)
}

func (s *RemoteGatePushSuite) TestPushGateWithoutARemoteGateRefusesAsToday() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})

	ev := s.push(head)
	s.Equal(gateAbsent, ev.cover)
	decision, reason := gradePushWithoutGate(ev, "")
	s.Equal(decisionDeny, decision)
	s.Contains(reason, "no `ci` run is recorded at this commit")
}
