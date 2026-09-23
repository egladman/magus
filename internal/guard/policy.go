package guard

import (
	"context"
	"os"
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
	// existing spawn rule records as this too: under stricter-of evaluation only its
	// tightening takes effect before approval.
	PolicyTightened = "tightened"
	// PolicyLoosenPending is an unapproved edit that removes the spawn rule, which has no
	// effect on a spawn until it is approved. A removed shell rule has no approved twin to
	// wait on, so it records as committed.
	PolicyLoosenPending = "loosen_pending"
	// PolicyCommitted is a rule set whose sources match the approved ones again, so what
	// was pending now applies in full.
	PolicyCommitted = "committed"
	// PolicyRemoved is a workspace with no guard rule left on either side.
	PolicyRemoved = "removed"
)

// policyMarkerFile holds the last recorded rule set, so a hook call learns whether the
// policy moved from one small read rather than by scanning the trail.
const policyMarkerFile = "guard-policy.json"

// PolicySource is one file a workspace guard rule can come from, named by the git blob id
// of its working-tree bytes and of its approved bytes. Approved is "" when the approved
// state has no such file.
type PolicySource struct {
	Path     string `json:"path"`
	Worktree string `json:"worktree"`
	Approved string `json:"approved"`
}

// PolicyState is the effective workspace rule set as one hook call sees it.
type PolicyState struct {
	// Digest names the rule set; "" when the working tree declares no guard rule.
	Digest     string
	ShellRules int
	SpawnRule  bool
	// Sources resolves each policy source's approved id. It can run a process per file, so
	// it is called only when the record can change; nil when there is no approval authority.
	Sources func(ctx context.Context) []PolicySource
}

type policyMarker struct {
	Digest     string `json:"digest"`
	ShellRules int    `json:"shell_rules"`
	SpawnRule  bool   `json:"spawn_rule"`
	Pending    bool   `json:"pending"`
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
	case seen && prev.Digest == now.Digest && (!recheck || !prev.Pending):
		return now.Digest
	}
	var sources []PolicySource
	if now.Sources != nil {
		sources = now.Sources(ctx)
	}
	pending := slices.ContainsFunc(sources, func(s PolicySource) bool { return s.Worktree != s.Approved })
	action := classifyPolicy(prev, seen, now, pending)
	next := policyMarker{Digest: now.Digest, ShellRules: now.ShellRules, SpawnRule: now.SpawnRule, Pending: pending}
	if action != "" {
		body, _ := json.Marshal(policyEvent{SchemaVersion: 1, Action: action, Digest: now.Digest, Previous: prev.Digest, Sources: sources})
		ref, size := trail.WriteBlob(ctx, cacheDir, "policy", body)
		trail.Append(ctx, cacheDir, trail.Event{
			Ts:           time.Now().UnixMilli(),
			Kind:         trail.KindGuardPolicy,
			Actor:        "guard",
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
	case prev.SpawnRule && !now.SpawnRule:
		return PolicyLoosenPending
	case now.ShellRules < prev.ShellRules:
		// Shell rules have no approved twin, so a dropped one is in effect at once.
		return PolicyCommitted
	}
	return PolicyTightened
}

// policyHadSpawnRule reports whether the last recorded policy carried a spawn rule, which
// is how a rule deleted from the working tree is still known to have an approved twin.
func policyHadSpawnRule(cacheDir string) bool {
	if cacheDir == "" {
		return false
	}
	m, ok := readPolicyMarker(filepath.Join(cacheDir, policyMarkerFile))
	return ok && m.SpawnRule
}

func readPolicyMarker(path string) (policyMarker, bool) {
	body, err := os.ReadFile(path)
	if err != nil {
		return policyMarker{}, false
	}
	var m policyMarker
	if json.Unmarshal(body, &m) != nil {
		return policyMarker{}, false
	}
	return m, true
}

// writePolicyMarker replaces the marker whole, so a racing hook reads the old record or
// the new one and never half of each. Losing the race costs a duplicate event, not a
// missed one.
func writePolicyMarker(path string, m policyMarker) {
	body, err := json.Marshal(m)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".guard-policy-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(body)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	if os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}
