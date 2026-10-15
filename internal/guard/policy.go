package guard

import (
	"bytes"
	"cmp"
	"context"
	"path/filepath"
	"slices"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
)

// The lineage actions a guard_policy trail event records.
const (
	// PolicyLoaded is the first effective rule set this cache has seen.
	PolicyLoaded = "loaded"
	// PolicyTightened is an unapproved edit whose added rules are live. An edit to an
	// existing spawn or command rule records as this too: under stricter-of evaluation only
	// its tightening takes effect before approval.
	PolicyTightened = "tightened"
	// PolicyLoosenPending is an unapproved edit that removes the spawn or command rule,
	// which has no effect until it is approved. A removed shell rule has no approved twin to
	// wait on, so it records as committed.
	PolicyLoosenPending = "loosen_pending"
	// PolicyCommitted is a rule set whose sources match the approved ones again, so what
	// was pending now applies in full.
	PolicyCommitted = "committed"
	// PolicyRemoved is a workspace with no guard rule left on either side.
	PolicyRemoved = "removed"
)

// policyMarkerFile holds the last recorded rule set, so a hook call learns whether the
// policy moved from one small read rather than by scanning the trail. It is a JSON Lines
// file whose last line is the marker.
const policyMarkerFile = "guard-policy.jsonl"

// oldPolicyMarkerFile is the JSON document an older magus kept the marker in.
const oldPolicyMarkerFile = "guard-policy.json"

// PolicySource is one file a workspace guard rule can come from, named by the ContentID of
// its working-tree bytes and of its approved bytes. Approved is "" when the approved
// state has no such file.
type PolicySource struct {
	Path     string `json:"path"`
	Worktree string `json:"worktree"`
	Approved string `json:"approved"`
}

// PolicyState is the effective workspace rule set as one hook call sees it.
type PolicyState struct {
	// Digest names the rule set; "" when the working tree declares no guard rule.
	Digest      string
	ShellRules  int
	SpawnRule   bool
	CommandRule bool
	WriteRule   bool
	// Files are the files the load read, the root magusfile and every source it imports, as
	// the load named them. Cheap, unlike Sources: they are what the load already logged.
	Files []string
	// Sources resolves each policy source's approved id. It can run a process per file, so
	// it is called only when the record can change; nil when there is no approval authority.
	Sources func(ctx context.Context) []PolicySource
}

type policyMarker struct {
	Digest      string `json:"digest"`
	ShellRules  int    `json:"shell_rules"`
	SpawnRule   bool   `json:"spawn_rule"`
	CommandRule bool   `json:"command_rule,omitempty"`
	WriteRule   bool   `json:"write_rule,omitempty"`
	Pending     bool   `json:"pending"`
	// Files is PolicyState.Files, kept so a call whose own load ran out of time still knows
	// which files the guard loads.
	Files []string `json:"files,omitempty"`
}

// policyMarkerLog is the marker's file: a line for each change of the rule set, the last one
// being the marker. The file is the guard's own state, private to its owner.
func policyMarkerLog(path string) jsonl[policyMarker] {
	return jsonl[policyMarker]{path: path, perm: 0o600, key: func(policyMarker) string { return "" }}
}

// policyEvent is the guard_policy blob: refs and ids, never a source body.
type policyEvent struct {
	SchemaVersion int            `json:"schema_version"`
	Action        string         `json:"action"`
	Digest        string         `json:"digest"`
	Previous      string         `json:"previous,omitempty"`
	Sources       []PolicySource `json:"sources,omitempty"`
}

// recordPolicy is RecordPolicy for the location a hook call resolved, "" when the caller
// could not describe the policy.
func recordPolicy(ctx context.Context, deps Dependencies, at location, recheck bool) string {
	if deps.Policy == nil {
		return ""
	}
	return RecordPolicy(ctx, at.cacheDir, at.workspace, deps.Policy(), recheck)
}

// RecordPolicy appends one guard_policy event when the effective workspace rules changed
// since the last call recorded, and returns the digest a verdict event carries.
//
// A steady-state call costs one small file read. recheck re-reads the approved side even
// when the digest is unchanged, but only while an edit is pending: approving an edit moves
// no working-tree byte, so a spawn, which pays for the approved side anyway, is where a
// pending edit is seen to settle.
func RecordPolicy(ctx context.Context, cacheDir, workspace string, now PolicyState, recheck bool) string {
	if cacheDir == "" {
		return now.Digest
	}
	path := filepath.Join(cacheDir, policyMarkerFile)
	prev, seen := readPolicyMarker(path)
	switch {
	case !seen && now.Digest == "":
		return now.Digest
	case seen && prev.Digest == now.Digest && (!recheck || !prev.Pending) && slices.Equal(prev.Files, now.Files):
		return now.Digest
	}
	var sources []PolicySource
	if now.Sources != nil {
		sources = now.Sources(ctx)
	}
	pending := slices.ContainsFunc(sources, func(s PolicySource) bool { return s.Worktree != s.Approved })
	action := classifyPolicy(prev, seen, now, pending)
	next := policyMarker{Digest: now.Digest, ShellRules: now.ShellRules, SpawnRule: now.SpawnRule, CommandRule: now.CommandRule, WriteRule: now.WriteRule, Pending: pending, Files: now.Files}
	if action != "" {
		body, _ := json.Marshal(policyEvent{SchemaVersion: 1, Action: action, Digest: now.Digest, Previous: prev.Digest, Sources: sources})
		ref, size := trail.WriteBlob(ctx, cacheDir, "policy", body)
		trail.Append(ctx, cacheDir, trail.Event{
			Ts:           time.Now().UnixMilli(),
			Kind:         trail.KindGuardPolicy,
			Workspace:    workspace,
			Action:       action,
			Outcome:      trail.OutcomeOK,
			RequestRef:   ref,
			RequestBytes: size,
			PolicyDigest: now.Digest,
		})
	}
	writePolicyMarker(path, next)
	return now.Digest
}

// classifyPolicy names what moved. With no approval authority the working tree is the
// whole policy and every change is in effect at once, which reads as committed.
func classifyPolicy(prev policyMarker, seen bool, now PolicyState, pending bool) string {
	switch {
	case !seen:
		return PolicyLoaded
	case now.Digest == prev.Digest:
		if prev.Pending && !pending {
			return PolicyCommitted
		}
		return ""
	case now.Digest == "" && !pending:
		return PolicyRemoved
	case !pending:
		return PolicyCommitted
	case prev.SpawnRule && !now.SpawnRule, prev.CommandRule && !now.CommandRule, prev.WriteRule && !now.WriteRule:
		return PolicyLoosenPending
	case now.ShellRules < prev.ShellRules:
		// Shell rules have no approved twin, so a dropped one is in effect at once.
		return PolicyCommitted
	}
	return PolicyTightened
}

// recordedRule reports whether the last rule set this cache recorded registered seam's
// rule. The marker is written only from a policy that loaded, so it outlives a load
// failure; false when there is no marker.
func recordedRule(cacheDir string, seam functionSeam) bool {
	if cacheDir == "" {
		return false
	}
	m, ok := readPolicyMarker(filepath.Join(cacheDir, policyMarkerFile))
	if !ok {
		return false
	}
	switch seam {
	case seamCommand:
		return m.CommandRule
	case seamWrite:
		return m.WriteRule
	}
	return m.SpawnRule
}

func readPolicyMarker(path string) (policyMarker, bool) {
	refuseOldJSON(filepath.Dir(path), oldPolicyMarkerFile)
	held, err := policyMarkerLog(path).load()
	if err != nil || len(held) == 0 {
		return policyMarker{}, false
	}
	return held[0], true
}

// writePolicyMarker appends the marker when it differs from the last one, so a racing hook
// reads the old record or the new one and never half of each. Losing the race costs a
// duplicate event, not a missed one.
func writePolicyMarker(path string, m policyMarker) {
	_ = policyMarkerLog(path).update(context.Background(), func(held []policyMarker) []policyMarker {
		// By encoding: a marker read back has nil where the one written had an empty list.
		if old, err := json.Marshal(held); len(held) == 1 && err == nil {
			if next, err := json.Marshal([]policyMarker{m}); err == nil && bytes.Equal(old, next) {
				return nil
			}
		}
		return []policyMarker{m}
	})
}

// isGuardSource reports whether path is a file the guard's own policy load reads: the
// workspace's root magusfile, or a source the policy loaded on this call or, when this
// call's load did not finish, the last one recorded. A relative path is the calling
// directory's, since the write it names lands there; a relative source is the workspace's.
// Paths compare with symlinks resolved, so /var and /private/var name one file.
func isGuardSource(deps Dependencies, at location, path string) bool {
	if at.workspace == "" || path == "" {
		return false
	}
	target, placed := placePath(path, cmp.Or(at.dir, at.workspace), userHome())
	if !placed {
		return false
	}
	if samePath(target, filepath.Join(at.workspace, "magusfile.buzz")) {
		return true
	}
	var files []string
	if deps.Policy != nil {
		files = deps.Policy().Files
	}
	if files == nil && at.cacheDir != "" {
		if m, ok := readPolicyMarker(filepath.Join(at.cacheDir, policyMarkerFile)); ok {
			files = m.Files
		}
	}
	return slices.ContainsFunc(files, func(f string) bool { return samePath(resolveFrom(at.workspace, f), target) })
}
