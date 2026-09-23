package guard

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sourcesAt builds the lazy approved-id callback a hook hands RecordPolicy, counting how
// often it runs: it costs a process per file, so steady state must not call it.
func sourcesAt(calls *int, pairs ...PolicySource) func(context.Context) []PolicySource {
	return func(context.Context) []PolicySource {
		*calls++
		return pairs
	}
}

func policyActions(t *testing.T, base string) []string {
	t.Helper()
	var out []string
	for _, e := range trailEvents(t, base, trail.KindGuardPolicy) {
		out = append(out, e.Action)
	}
	return out
}

// One event per change and none on a steady state, which is what makes the trail read
// as the policy's lineage rather than as a log of hook calls.
func TestRecordPolicyWritesOneEventPerChange(t *testing.T) {
	base := t.TempDir()
	calls := 0
	clean := sourcesAt(&calls, PolicySource{Path: "/w/magusfile.buzz", Worktree: "a", Approved: "a"})

	assert.Empty(t, RecordPolicy(t.Context(), base, "/w", PolicyState{}, false), "no rules, nothing to record")
	assert.Empty(t, policyActions(t, base))

	state := PolicyState{Digest: "d1", SpawnRule: true, Sources: clean}
	assert.Equal(t, "d1", RecordPolicy(t.Context(), base, "/w", state, false))
	for range 5 {
		RecordPolicy(t.Context(), base, "/w", state, false)
		RecordPolicy(t.Context(), base, "/w", state, true)
	}
	assert.Equal(t, []string{PolicyLoaded}, policyActions(t, base))
	assert.Equal(t, 1, calls, "a steady state with nothing pending reads no approved ids")
}

// The lineage an uncommitted edit leaves: tightened when it adds, loosen_pending when it
// removes, committed once the approved sources catch up, removed when nothing is left.
func TestRecordPolicyClassifiesTheChange(t *testing.T) {
	base := t.TempDir()
	calls := 0
	clean := sourcesAt(&calls, PolicySource{Path: "/w/magusfile.buzz", Worktree: "a", Approved: "a"})
	dirty := sourcesAt(&calls, PolicySource{Path: "/w/magusfile.buzz", Worktree: "b", Approved: "a"})
	ctx := t.Context()

	RecordPolicy(ctx, base, "/w", PolicyState{Digest: "d1", ShellRules: 1, Sources: clean}, false)
	RecordPolicy(ctx, base, "/w", PolicyState{Digest: "d2", ShellRules: 1, SpawnRule: true, Sources: dirty}, false)
	RecordPolicy(ctx, base, "/w", PolicyState{Digest: "d3", ShellRules: 1, Sources: dirty}, false)

	before := calls
	RecordPolicy(ctx, base, "/w", PolicyState{Digest: "d3", ShellRules: 1, Sources: clean}, false)
	assert.Equal(t, before, calls, "a command call does not re-read a pending edit")
	RecordPolicy(ctx, base, "/w", PolicyState{Digest: "d3", ShellRules: 1, Sources: clean}, true)

	RecordPolicy(ctx, base, "/w", PolicyState{Sources: clean}, false)

	assert.Equal(t, []string{PolicyLoaded, PolicyTightened, PolicyLoosenPending, PolicyCommitted, PolicyRemoved}, policyActions(t, base))
}

// Shell rules have no approved twin, so dropping one applies at once even while another
// edit is pending.
func TestRecordPolicyShellRuleDropIsCommitted(t *testing.T) {
	base := t.TempDir()
	calls := 0
	dirty := sourcesAt(&calls, PolicySource{Path: "/w/magusfile.buzz", Worktree: "b", Approved: "a"})
	RecordPolicy(t.Context(), base, "/w", PolicyState{Digest: "d1", ShellRules: 2, SpawnRule: true, Sources: dirty}, false)
	RecordPolicy(t.Context(), base, "/w", PolicyState{Digest: "d2", ShellRules: 1, SpawnRule: true, Sources: dirty}, false)
	assert.Equal(t, []string{PolicyLoaded, PolicyCommitted}, policyActions(t, base))
}

// A guard_policy event names sources by blob id and carries both digests, never a body.
func TestRecordPolicyEventCarriesRefsNotBodies(t *testing.T) {
	base := t.TempDir()
	calls := 0
	src := PolicySource{Path: "/w/magusfile.buzz", Worktree: "b", Approved: "a"}
	RecordPolicy(t.Context(), base, "/w", PolicyState{Digest: "d1", SpawnRule: true, Sources: sourcesAt(&calls)}, false)
	RecordPolicy(t.Context(), base, "/w", PolicyState{Digest: "d2", SpawnRule: true, Sources: sourcesAt(&calls, src)}, false)

	events := trailEvents(t, base, trail.KindGuardPolicy)
	require.Len(t, events, 2)
	var tightened trail.Event
	for _, e := range events {
		if e.Action == PolicyTightened {
			tightened = e
		}
	}
	require.NotEmpty(t, tightened.RequestRef)
	assert.Equal(t, "d2", tightened.PolicyDigest)
	body, err := trail.ReadBlob(base, tightened.RequestRef)
	require.NoError(t, err)
	var got policyEvent
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, policyEvent{SchemaVersion: 1, Action: PolicyTightened, Digest: "d2", Previous: "d1", Sources: []PolicySource{src}}, got)
}

// Every verdict event names the policy in force and which side decided it.
func TestVerdictEventsCarryThePolicy(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	deps := Dependencies{Policy: func() PolicyState { return PolicyState{Digest: "d9", ShellRules: 1} }}

	Judge(ctx, deps, Request{Input: "git stash", Host: "claude-code", Session: "s1"})
	commands := trailEvents(t, cacheDir, trail.KindAgentCommand)
	require.Len(t, commands, 1)
	assert.Equal(t, "d9", commands[0].PolicyDigest)
	assert.Equal(t, decidedByBuiltin, commands[0].DecidedBy)

	deps.ShellRules = []WorkspaceShellRule{{Name: "no-curl", Decision: "deny", Program: "curl", Reason: "not here"}}
	Judge(ctx, deps, Request{Input: "curl https://example.com", Host: "claude-code", Session: "s1"})
	commands = trailEvents(t, cacheDir, trail.KindAgentCommand)
	require.Len(t, commands, 2)
	var byWorkspace int
	for _, e := range commands {
		if e.DecidedBy == decidedByWorktree {
			byWorkspace++
		}
	}
	assert.Equal(t, 1, byWorkspace, "a workspace shell rule's deny is the working tree's")
}
