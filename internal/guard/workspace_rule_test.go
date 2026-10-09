package guard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errStaleLoad is what a ./magus older than the tree reports: it cannot load the working tree,
// and parsing the approved copy with the same binary fails the same way.
var errStaleLoad = errors.New(`magusfile: exec magusfile.buzz: [BZZ2001] buzz: import "./hack/policy/guard": buzz: line 73:14: object CommandInvocation has no field or method "vcs"`)

// unloadedDeps is a workspace whose working tree and approved copy both fail to load the
// way a binary older than the tree does.
func unloadedDeps() Dependencies { return unloadedDepsFor(errStaleLoad) }

// unloadedDepsFor is unloadedDeps failing with loadErr on both sides.
func unloadedDepsFor(loadErr error) Dependencies {
	approvedErr := errors.New("approved magusfile: " + loadErr.Error())
	return Dependencies{
		LoadFailure:         loadErr,
		ApprovedCommandRule: func(context.Context) (workspace.CommandRule, error) { return nil, approvedErr },
		ApprovedSpawnRule:   func(context.Context) (workspace.SpawnRule, error) { return nil, approvedErr },
		ApprovedWriteRule:   func(context.Context) (workspace.WriteRule, error) { return nil, approvedErr },
	}
}

// staleFailures are the failures unloadedDeps reports on either seam.
var staleFailures = []trail.RuleFailure{
	{Side: decidedByWorktree, Error: "the magusfile failed to load: " + errStaleLoad.Error()},
	{Side: decidedByApproved, Error: "the rule could not be resolved: approved magusfile: " + errStaleLoad.Error()},
}

// recordLoadedPolicy leaves the marker a hook call writes after a policy loads.
func recordLoadedPolicy(t *testing.T, cacheDir string) {
	t.Helper()
	RecordPolicy(t.Context(), cacheDir, "/w", PolicyState{Digest: "d1", SpawnRule: true, CommandRule: true, WriteRule: true}, false)
	require.FileExists(t, filepath.Join(cacheDir, policyMarkerFile))
}

// staleDeny is the deny for call while a binary that cannot load the tree judges it, in a
// checkout of root with no ./magus of its own.
func staleDeny(call unloadedCall, root string) string {
	return staleBinaryReason(call, causeStale, failureLines(staleFailures), false, root)
}

// With a recorded rule set and neither side loading, the calls the policy exists to judge
// are denied with the fix, decided by the guard itself.
func TestLoadFailureDeniesGatedVerbs(t *testing.T) {
	cases := []struct{ line, verb string }{
		{"gh pr merge 412 --squash --admin", "`gh pr merge`"},
		{"git push origin HEAD:topic", "`git push`"},
		{"git -C ../other push", "`git push`"},
		{"jj git push -b topic", "`jj git push`"},
		{"./magus init --vcs git", "`magus init`"},
		{"magus -s server start", "`magus server start`"},
		{"./magus config set --global key=log.format,value=json", "`magus config set`"},
		{"./magus agent install --global /home/me/.claude/skills", "`magus agent install`"},
		{"./magus agent harness install", "`magus agent harness install`"},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			ctx, cacheDir := spawnFixture(t)
			recordLoadedPolicy(t, cacheDir)

			v := Judge(ctx, unloadedDeps(), Request{Input: tc.line, Host: "claude-code", Session: "s1"})
			assert.Equal(t, Verdict{
				SchemaVersion: agent.GuardSchemaVersion,
				Decision:      "deny",
				Reason: staleDeny(unloadedCall{seam: seamCommand, verb: tc.verb, what: tc.verb, changes: true},
					hookLocation(ctx, Dependencies{}).workspace),
				Rule: workspaceCommandRule,
			}, v)

			commands := trailEvents(t, cacheDir, trail.KindAgentCommand)
			require.Len(t, commands, 1)
			assert.Equal(t, decidedByBuiltin, commands[0].DecidedBy)
		})
	}
}

// ownCheckout makes the fixture's root a checkout of magus itself, with a ./magus when
// hasBinary, and returns the root.
func ownCheckout(t *testing.T, ctx context.Context, hasBinary bool) string {
	t.Helper()
	root := hookLocation(ctx, Dependencies{}).workspace
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+ownModule+"\n\ngo 1.25\n"), 0o644))
	if hasBinary {
		require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte("#!/bin/sh\n"), 0o755))
	}
	return root
}

// In a checkout of magus itself the fix is the rebuild, and the relink for a binary that
// cannot load the tree to rebuild itself.
func TestLoadFailureDenyNamesTheRebuildInMagusOwnCheckout(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	recordLoadedPolicy(t, cacheDir)
	root := ownCheckout(t, ctx, true)

	v := Judge(ctx, unloadedDeps(), Request{Input: "git push", Host: "claude-code", Session: "s1"})
	assert.Equal(t, Verdict{
		SchemaVersion: agent.GuardSchemaVersion,
		Decision:      "deny",
		Reason: "magus workspace: `git push` is denied because the magus judging it cannot load this workspace, so its guard policy is not running and nothing could say the call is safe. " +
			"That magus is older than the tree.\n" +
			"  worktree: the magusfile failed to load: " + errStaleLoad.Error() + "\n" +
			"  approved: the rule could not be resolved: approved magusfile: " + errStaleLoad.Error() + "\n" +
			"Edits, spawns, pushes and commands that change state wait until a binary loads the tree; reads, `git status` and the fix still run.\n" +
			"Rebuild it: `./magus run go-build .`. " +
			"If that cannot load the tree either, move it aside and bootstrap, one command at a time: `mv magus magus.old`, " +
			"`GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .`. If the error names a magusfile line instead, fix that line.",
		Rule: workspaceCommandRule,
	}, v)
	assert.Equal(t, staleBinaryReason(unloadedCall{seam: seamCommand, what: "`git push`"}, causeStale, failureLines(staleFailures), true, root), v.Reason)
}

// A checkout with no ./magus is bootstrapped, not rebuilt: there is nothing to move aside.
func TestLoadFailureDenyNamesTheBootstrapWhereThereIsNoBinary(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	recordLoadedPolicy(t, cacheDir)
	ownCheckout(t, ctx, false)

	v := Judge(ctx, unloadedDeps(), Request{Input: "git push", Host: "claude-code", Session: "s1"})
	require.Equal(t, "deny", v.Decision)
	assert.Contains(t, v.Reason, "This checkout has no ./magus. Bootstrap one: `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .`.")
	assert.NotContains(t, v.Reason, "mv magus magus.old")
}

// A leased worker is never told to build: the orchestrator places one binary per base.
func TestLoadFailureDenyNamesTheOrchestratorForALeasedWorker(t *testing.T) {
	ctx, _ := fleetFixture(t, fleetLeases()[0])
	recordLoadedPolicy(t, hookLocation(ctx, Dependencies{}).cacheDir)
	ownCheckout(t, ctx, true)

	v := Judge(ctx, unloadedDeps(), Request{Input: "git push", Host: "claude-code", Session: "s1", Lease: "lease-a"})
	require.Equal(t, "deny", v.Decision)
	assert.Contains(t, v.Reason, "ask it to run `magus buzz hack/dev/bootstrap-worktree.buzz -- --job lease-a`")
	assert.NotContains(t, v.Reason, "go-build")
	assert.NotContains(t, v.Reason, "Rebuild it")
}

// An error in the magusfile that no newer binary would fix, in a checkout that has its
// own ./magus, stays the gated verbs' deny and leaves every other call open.
func TestLoadFailureOfATypoDeniesOnlyGatedVerbs(t *testing.T) {
	typo := errors.New("magusfile.buzz:3:1: expected expression")
	failures := []trail.RuleFailure{
		{Side: decidedByWorktree, Error: "the magusfile failed to load: " + typo.Error()},
		{Side: decidedByApproved, Error: "the rule could not be resolved: approved magusfile: " + typo.Error()},
	}
	ctx, cacheDir := spawnFixture(t)
	recordLoadedPolicy(t, cacheDir)
	ownCheckout(t, ctx, true)
	deps := unloadedDepsFor(typo)

	push := Judge(ctx, deps, Request{Input: "git push", Host: "claude-code", Session: "s1"})
	assert.Equal(t, "deny", push.Decision)
	assert.Equal(t, unloadedReason(unloadedCall{seam: seamCommand, verb: "`git push`"}, failures, true, true), push.Reason)
	assert.Contains(t, push.Reason, "The likeliest cause is a ./magus older than the tree. Rebuild it: `./magus run go-build .`.")

	other := Judge(ctx, deps, Request{Input: "ls -la", Host: "claude-code", Session: "s1"})
	assert.Equal(t, verdictWithRule("advise", string(advisoryCommandRuleFailed)), unworded(other))
	change := Judge(ctx, deps, Request{Input: "touch NOTES.md", Host: "claude-code", Session: "s1"})
	assert.NotEqual(t, "deny", change.Decision, "a typo leaves state-changing commands on the built-in rules")
}

// Every other command, the fix included, passes with the note that the policy judged
// nothing.
func TestLoadFailurePassesOtherCommandsWithTheNote(t *testing.T) {
	note := "The working tree's magus\\guard.command rule judged nothing: " + staleFailures[0].Error + "\n\n" +
		"The approved magus\\guard.command rule judged nothing: " + staleFailures[1].Error + "\n\n" +
		"Only the built-in rules applied to this command."
	for _, line := range []string{
		"ls -la",
		"./magus run go-build .",
		"mv magus magus.old",
		"./magus init --dry-run",
		"git status",
		"gh pr view 412",
	} {
		t.Run(line, func(t *testing.T) {
			ctx, cacheDir := spawnFixture(t)
			recordLoadedPolicy(t, cacheDir)

			v := Judge(ctx, unloadedDeps(), Request{Input: line, Host: "claude-code", Session: "s1"})
			assert.Equal(t, verdictWithRule("advise", string(advisoryCommandRuleFailed)), unworded(v))
			assert.Contains(t, v.Context, note)
		})
	}
	t.Run("told in full once, then in one line", func(t *testing.T) {
		ctx, cacheDir := spawnFixture(t)
		recordLoadedPolicy(t, cacheDir)
		first := Judge(ctx, unloadedDeps(), Request{Input: "ls", Host: "claude-code", Session: "s1"})
		assert.Equal(t, Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "advise", Context: note, Rule: string(advisoryCommandRuleFailed)}, first)
		repeat := Judge(ctx, unloadedDeps(), Request{Input: "ls", Host: "claude-code", Session: "s1"})
		assert.Equal(t, Verdict{
			SchemaVersion: agent.GuardSchemaVersion, Decision: "advise", Rule: string(advisoryCommandRuleFailed),
			Context: "Only the built-in rules applied to this command. A workspace command rule is still failing, as told earlier in this session.",
		}, repeat)
	})
}

// No record means no rule was ever seen to protect: a workspace whose policy never loaded
// here is not held to one.
func TestLoadFailureWithoutARecordPasses(t *testing.T) {
	ctx, _ := spawnFixture(t)
	v := Judge(ctx, unloadedDeps(), Request{Input: "gh pr merge 412 --admin", Host: "claude-code", Session: "s1"})
	assert.Equal(t, verdictWithRule("advise", string(advisoryCommandRuleFailed)), unworded(v))
}

// A record that registered no command rule protects no command.
func TestLoadFailureOfAnUnregisteredSeamPasses(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	RecordPolicy(t.Context(), cacheDir, "/w", PolicyState{Digest: "d1", SpawnRule: true}, false)
	v := Judge(ctx, unloadedDeps(), Request{Input: "git push", Host: "claude-code", Session: "s1"})
	assert.Equal(t, "advise", v.Decision)
}

// A rule that loaded and then raised stays fail-open with its notice: only a policy that
// cannot be read at all is the misconfiguration.
func TestLoadFailureOfOneRuleStaysOpen(t *testing.T) {
	raises := (&commandRuleProbe{err: errors.New("magus\\guard.command: the rule raised: boom")}).rule()
	cases := []struct {
		name string
		deps Dependencies
	}{
		{"the working tree's rule raises", Dependencies{CommandRule: raises}},
		{"the approved rule raises under a broken working tree", Dependencies{
			LoadFailure:         errStaleLoad,
			ApprovedCommandRule: func(context.Context) (workspace.CommandRule, error) { return raises, nil },
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cacheDir := spawnFixture(t)
			recordLoadedPolicy(t, cacheDir)
			v := Judge(ctx, tc.deps, Request{Input: "gh pr merge 412 --admin", Host: "claude-code", Session: "s1"})
			assert.Equal(t, "advise", v.Decision)
			assert.Contains(t, v.Context, "the rule raised: boom")
		})
	}
}

// A spawn is gated; a continuation of a child already running is not.
func TestLoadFailureDeniesASpawn(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	recordLoadedPolicy(t, cacheDir)

	v := Judge(ctx, unloadedDeps(), Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	assert.Equal(t, Verdict{
		SchemaVersion: agent.GuardSchemaVersion,
		Decision:      "deny",
		Reason: staleDeny(unloadedCall{seam: seamSpawn, verb: "a subagent spawn", what: "a subagent spawn", changes: true},
			hookLocation(ctx, Dependencies{}).workspace),
		Rule: workspaceSpawnRule,
	}, v)
	spawns := trailEvents(t, cacheDir, trail.KindAgentSpawn)
	require.Len(t, spawns, 1)
	assert.Equal(t, decidedByBuiltin, spawns[0].DecidedBy)
	assert.Equal(t, staleFailures, readSpawnBlob(t, cacheDir, spawns[0]).RuleFailures)

	cont := Judge(ctx, unloadedDeps(), Request{Input: claudeContinueEnvelope, Host: "claude-code"})
	assert.Equal(t, verdictWithRule("advise", string(advisorySpawnRuleFailed)), unworded(cont))
}

// A file write waits too while the binary cannot load the tree: the fix is a rebuild, not
// an edit.
func TestLoadFailureDeniesAWrite(t *testing.T) {
	ctx, root, cacheDir := writeFixture(t)
	recordLoadedPolicy(t, cacheDir)
	v := Judge(ctx, unloadedDeps(), Request{Input: filepath.Join(root, "CHANGELOG.md"), IsPath: true, Host: "claude-code", Session: "s1"})
	assert.Equal(t, Verdict{
		SchemaVersion: agent.GuardSchemaVersion,
		Decision:      "deny",
		Reason:        staleDeny(unloadedCall{seam: seamWrite, what: "this file write", changes: true}, root),
		Rule:          workspaceWriteRule,
	}, v)
}

// A load failure no newer binary fixes leaves the write on the built-in rules, with the note.
func TestLoadFailureOfATypoPassesAWriteWithTheNote(t *testing.T) {
	ctx, root, cacheDir := writeFixture(t)
	recordLoadedPolicy(t, cacheDir)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte("#!/bin/sh\n"), 0o755))
	v := Judge(ctx, unloadedDepsFor(errors.New("magusfile.buzz:3:1: expected expression")), Request{Input: filepath.Join(root, "CHANGELOG.md"), IsPath: true, Host: "claude-code", Session: "s1"})
	assert.Equal(t, verdictWithRule("advise", string(advisoryWriteRuleFailed)), unworded(v))
	assert.Contains(t, v.Context, "Only the built-in rules applied to this write.")
}

// A deny or an ask reached with a rule missing carries the note, so neither reads as
// judged by the whole policy.
func TestWorkspaceRuleFailureNoteRidesEveryVerdict(t *testing.T) {
	cases := []struct {
		name     string
		in, want Verdict
	}{
		{"a deny", Verdict{Decision: "deny", Reason: "no", Rule: "r"}, Verdict{Decision: "deny", Reason: "no\n\nnote", Rule: "r"}},
		{"an ask", Verdict{Decision: "ask", Reason: "approve?", Rule: "r"}, Verdict{Decision: "ask", Reason: "approve?\n\nnote", Rule: "r"}},
		{"an advise", Verdict{Decision: "advise", Context: "hint", Rule: "r"}, Verdict{Decision: "advise", Context: "hint\n\nnote", Rule: "r"}},
		{"a pass", Verdict{Decision: "pass"}, Verdict{Decision: "advise", Context: "note", Rule: string(advisoryCommandRuleFailed)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, applyRuleFailureNote(tc.in, "note", advisoryCommandRuleFailed))
		})
	}
	assert.Equal(t, Verdict{Decision: "deny", Reason: "no"}, applyRuleFailureNote(Verdict{Decision: "deny", Reason: "no"}, "", advisoryCommandRuleFailed))
}

// An approved deny under a broken working tree names what did not run.
func TestWorkspaceRuleApprovedDenyCarriesTheNote(t *testing.T) {
	ctx, _ := spawnFixture(t)
	approved := (&commandRuleProbe{answer: types.GuardVerdict{Decision: types.GuardDeny, Reason: "no watching"}}).rule()
	deps := Dependencies{
		LoadFailure:         errStaleLoad,
		ApprovedCommandRule: func(context.Context) (workspace.CommandRule, error) { return approved, nil },
	}
	v := Judge(ctx, deps, Request{Input: "gh run watch 1", Host: "claude-code", Session: "s1"})
	assert.Equal(t, Verdict{
		SchemaVersion: agent.GuardSchemaVersion,
		Decision:      "deny",
		Reason: "no watching\n\nThe working tree's magus\\guard.command rule judged nothing: " + staleFailures[0].Error + "\n\n" +
			"Only the built-in rules and the approved magus\\guard.command rule applied to this command.",
		Rule: workspaceCommandRule,
	}, v)
}
