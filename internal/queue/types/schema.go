package types

import (
	"errors"
	"fmt"
	"time"
)

// Schema names. Each document carries its own under "schema"; a reader refuses any
// other, so a format change is a new name rather than a silent misread.
const (
	SchemaChanges = "mergequeue.changes/v1"
	SchemaPlan    = "mergequeue.plan/v1"
	SchemaVerdict = "mergequeue.verdict/v1"
	// SchemaCapabilities names what `magus queue describe` prints: a provider's
	// [Capabilities] on one base.
	SchemaCapabilities = "mergequeue.capabilities/v1"
)

// Changes is the queue's input: the changes carrying merge intent, in queue order.
type Changes struct {
	Schema    string   `json:"schema"`
	Base      string   `json:"base"`                 // branch the queue merges into
	RemoteURL string   `json:"remote_url,omitempty"` // URL of the remote the provider was asked about
	Changes   []Change `json:"changes"`
	// Merged are merged changes an open one may be stacked on. Planning checks each
	// one's commit is on the base before relying on it.
	Merged []MergedChange `json:"merged,omitempty"`
	// Unqueued are the open changes carrying no merge intent.
	Unqueued []UnqueuedChange `json:"unqueued,omitempty"`
	// Closed are the closed changes still showing a queue label.
	Closed []ClosedChange `json:"closed,omitempty"`
}

// Check reports whether c is a document the queue can plan: a valid base, every record
// valid, no id twice, and no two changes at one head.
func (c Changes) Check() error {
	if err := checkBranch(c.Base); err != nil {
		return fmt.Errorf("base: %w", err)
	}
	seen := make(map[string]bool, len(c.Changes)+len(c.Merged)+len(c.Unqueued))
	heads := make(map[string]string, len(c.Changes))
	for i, ch := range c.Changes {
		if err := ch.Check(); err != nil {
			return fmt.Errorf("changes[%d]: %w", i, err)
		}
		if seen[ch.ID] {
			return fmt.Errorf("changes[%d]: id %q appears twice", i, ch.ID)
		}
		seen[ch.ID] = true
		// Two changes at one head would each read as stacked on the other.
		if other, ok := heads[ch.Head]; ok {
			return fmt.Errorf("changes[%d]: #%s and #%s share head %s", i, other, ch.ID, ch.Head)
		}
		heads[ch.Head] = ch.ID
	}
	for i, m := range c.Merged {
		if err := m.check(); err != nil {
			return fmt.Errorf("merged[%d]: %w", i, err)
		}
		if seen[m.ID] {
			return fmt.Errorf("merged[%d]: id %q appears twice", i, m.ID)
		}
		seen[m.ID] = true
	}
	for i, u := range c.Unqueued {
		if err := u.check(); err != nil {
			return fmt.Errorf("unqueued[%d]: %w", i, err)
		}
		if seen[u.ID] {
			return fmt.Errorf("unqueued[%d]: id %q appears twice", i, u.ID)
		}
		seen[u.ID] = true
	}
	for i, cl := range c.Closed {
		if err := CheckID(cl.ID); err != nil {
			return fmt.Errorf("closed[%d]: %w", i, err)
		}
		if seen[cl.ID] {
			return fmt.Errorf("closed[%d]: id %q appears twice", i, cl.ID)
		}
		seen[cl.ID] = true
	}
	return nil
}

// Decision is what the queue decided for one change.
type Decision string

const (
	// DecisionMerge: validated green; merge once every change beneath it has.
	DecisionMerge Decision = "merge"
	// DecisionKick: a real conflict, a red gate, or a refusal; kick back with the report.
	DecisionKick Decision = "kick"
	// DecisionWait: not approved, or held behind a conflicting change; retried next run.
	DecisionWait Decision = "wait"
	// DecisionMerged: its head is already on the base, so nothing is left to do.
	DecisionMerged Decision = "merged"
)

// Code says why a change waits or was kicked back, from a closed set a provider and a
// workflow can act on. A CodeWait constant goes with [DecisionWait] and a CodeKick
// constant with [DecisionKick].
type Code string

const (
	CodeWaitNotApproved     Code = "WAIT_NOT_APPROVED"     // no approval at the commit a review of its head covers
	CodeWaitHeadMoved       Code = "WAIT_HEAD_MOVED"       // its head moved since it was listed or validated
	CodeWaitBehind          Code = "WAIT_BEHIND"           // a change validated beneath it did not merge, or it was not validated this run
	CodeWaitConflictAhead   Code = "WAIT_CONFLICT_AHEAD"   // it conflicts with a change ahead of it, which merges first
	CodeWaitRevalidate      Code = "WAIT_REVALIDATE"       // what it was validated on is no longer what it would merge onto
	CodeWaitBranchMoved     Code = "WAIT_BRANCH_MOVED"     // its branch moved or was deleted before an update commit could be pushed
	CodeWaitProviderRefused Code = "WAIT_PROVIDER_REFUSED" // the provider refused the merge
	CodeWaitBelow           Code = "WAIT_BELOW"            // the change it is stacked on has not merged
	CodeWaitBelowKicked     Code = "WAIT_BELOW_KICKED"     // the change it is stacked on was kicked back
	CodeWaitRestack         Code = "WAIT_RESTACK"          // it is not built on the head of the change it says it is stacked on
	CodeWaitRetarget        Code = "WAIT_RETARGET"         // it targets another branch than the queue's base
	CodeWaitMethodChanged   Code = "WAIT_METHOD_CHANGED"   // its merge method changed since validation
	CodeWaitWithdrawn       Code = "WAIT_WITHDRAWN"        // its merge intent was withdrawn since it was listed
	CodeWaitUnqueuedBelow   Code = "WAIT_UNQUEUED_BELOW"   // it carries the commits of an open change nobody queued
	CodeWaitNoCommitter     Code = "WAIT_NO_COMMITTER"     // it needs an update commit, and nothing names who commits it
	CodeWaitBaseRed         Code = "WAIT_BASE_RED"         // the gate was red on its candidate and on what that was built onto
	CodeWaitChecks          Code = "WAIT_CHECKS"           // the base's required checks are running on its head, or run again on the base

	CodeKickConflict Code = "KICK_CONFLICT" // a real conflict with the base in files that are not generated
	CodeKickRed      Code = "KICK_RED"      // the gate, or a required check on a head carrying the base, was red
	CodeKickRefused  Code = "KICK_REFUSED"  // something the author has to fix that is neither
	// CodeKickRegeneration: its own code changes what regenerates generated files it
	// needs regenerated, so the queue cannot prove that regeneration runs none of it, and
	// validation left no regenerated candidate applying can check.
	CodeKickRegeneration Code = "KICK_REGENERATION"
)

// decision is the decision c goes with, or "" for a code outside the set.
func (c Code) decision() Decision {
	switch c {
	case CodeWaitNotApproved, CodeWaitHeadMoved, CodeWaitBehind, CodeWaitConflictAhead, CodeWaitRevalidate,
		CodeWaitBranchMoved, CodeWaitProviderRefused, CodeWaitBelow, CodeWaitBelowKicked, CodeWaitRestack,
		CodeWaitRetarget, CodeWaitMethodChanged, CodeWaitWithdrawn, CodeWaitUnqueuedBelow, CodeWaitNoCommitter,
		CodeWaitBaseRed, CodeWaitChecks:
		return DecisionWait
	case CodeKickConflict, CodeKickRed, CodeKickRefused, CodeKickRegeneration:
		return DecisionKick
	}
	return ""
}

// Plan is what validation builds candidates for and an applier merges against.
type Plan struct {
	Schema     string `json:"schema"`
	Base       string `json:"base"`
	RemoteURL  string `json:"remote_url,omitempty"` // URL of the remote the provider names its repository by
	BaseCommit string `json:"base_commit"`          // tip of Base every candidate is built on
	// CommitDate dates every commit the queue writes: the newest commit date of the base
	// commit and each admitted head. It is read from the commits rather than a clock, so
	// the same queue plans the same candidates and nothing a cache keys on moves, and no
	// admitted commit is newer, so a build reading HEAD's date as "now" finds none ahead.
	CommitDate time.Time `json:"commit_date"`
	// Depth is how many candidates of one partition validate at once.
	Depth int `json:"depth"`
	// Partitions hold the admitted changes in queue order, every change after the one
	// it is stacked on. Changes in one partition stack; separate partitions share no
	// affected unit and never wait on each other.
	Partitions [][]Change `json:"partitions"`
	// Verdicts are the changes planning settled without validating them.
	Verdicts []Verdict `json:"verdicts,omitempty"`
	// Merged are the merged changes planning checked against the base, and Unqueued the
	// open changes carrying no intent, which an applier reads again when a rebased
	// head's approval has to be carried over.
	Merged   []MergedChange   `json:"merged,omitempty"`
	Unqueued []UnqueuedChange `json:"unqueued,omitempty"`
	// Closed are the listing's closed changes still showing a queue label, for an applier
	// to clear.
	Closed []ClosedChange `json:"closed,omitempty"`
}

// Check reports whether p is a plan the queue can validate and apply: a valid base and
// base commit, every change once, each after the change it is stacked on, and planning's
// verdicts deciding anything but merge, with no gate, and a commit date.
func (p Plan) Check() error {
	if err := checkBranch(p.Base); err != nil {
		return fmt.Errorf("base: %w", err)
	}
	if !IsObjectID(p.BaseCommit) {
		return fmt.Errorf("base commit %q is not a commit id", p.BaseCommit)
	}
	if p.Depth < 1 {
		return fmt.Errorf("depth %d is below 1", p.Depth)
	}
	seen := map[string]bool{}
	add := func(c Change) error {
		if err := c.Check(); err != nil {
			return err
		}
		if seen[c.ID] {
			return fmt.Errorf("change %q appears twice", c.ID)
		}
		seen[c.ID] = true
		return nil
	}
	for _, g := range p.Partitions {
		ahead := make(map[string]bool, len(g))
		for _, c := range g {
			// A change merges after the one it is stacked on, so that one is earlier in
			// the same partition.
			if c.Below != "" && !ahead[c.Below] {
				return fmt.Errorf("%s is stacked on #%s, which is not ahead of it in its partition", c.Label(), c.Below)
			}
			if err := add(c); err != nil {
				return err
			}
			ahead[c.ID] = true
		}
	}
	for _, v := range p.Verdicts {
		if err := add(v.Change); err != nil {
			return err
		}
		switch {
		case v.Decision == DecisionMerge:
			return fmt.Errorf("planning decides merge for %s, which only validation decides", v.Change.Label())
		case v.Gate != "":
			return fmt.Errorf("planning's verdict on %s names a gate, which only validation runs", v.Change.Label())
		}
		if err := v.Check(); err != nil {
			return err
		}
	}
	for _, m := range p.Merged {
		if err := m.check(); err != nil {
			return fmt.Errorf("merged: %w", err)
		}
	}
	for _, u := range p.Unqueued {
		if err := u.check(); err != nil {
			return fmt.Errorf("unqueued: %w", err)
		}
	}
	for _, cl := range p.Closed {
		if err := CheckID(cl.ID); err != nil {
			return fmt.Errorf("closed: %w", err)
		}
	}
	if p.CommitDate.IsZero() {
		return errors.New("the plan records no commit date")
	}
	return nil
}

// Verdict is what the queue decided for one change. Validation writes one per admitted
// change as a document of its own; planning's ride in [Plan.Verdicts].
type Verdict struct {
	Schema     string   `json:"schema,omitempty"`
	BaseCommit string   `json:"base_commit,omitempty"`
	Change     Change   `json:"change"`
	Decision   Decision `json:"decision"`
	Code       Code     `json:"code,omitempty"` // set on every wait and kick
	Reason     string   `json:"reason,omitempty"`
	Report     string   `json:"report,omitempty"` // kick-back body
	Paths      []string `json:"paths,omitempty"`  // the files a kick-back is about
	With       []string `json:"with,omitempty"`   // base-branch commits touching Paths
	// After is the change validated beneath this one, empty at the bottom of its
	// partition. An applier holds a change whose After did not merge.
	After string `json:"after,omitempty"`
	// Onto is the commit the candidate was built onto: BaseCommit at the bottom of a
	// partition, else After's candidate.
	Onto string `json:"onto,omitempty"`
	// CandidateCommit is the validated merge commit: base plus every change beneath this
	// one plus this one, generated files regenerated. An applier builds it again from
	// the change's head and the base's own regeneration, and merges only what matches.
	CandidateCommit string      `json:"candidate_commit,omitempty"`
	Method          MergeMethod `json:"method,omitempty"` // the merge method it was validated under
	// Depth is the candidate's speculation depth when its gate started: 1 ran on
	// validated commits alone, 2 on top of one unvalidated candidate, and so on.
	Depth      int   `json:"depth,omitempty"`
	DurationMS int64 `json:"duration_ms,omitempty"` // gate wall time
	// Gate and Regenerate are the hook command lines validation ran, recorded so a
	// kick-back can say how to run them again; planning's verdicts carry neither.
	Gate       string `json:"gate,omitempty"`
	Regenerate string `json:"regenerate,omitempty"`
}

// Check reports whether v is a verdict an applier can act on: a valid change, commit ids
// where it names commits, a regeneration only beside a gate, a code of its decision's
// class on every wait and kick, and what a merge needs rebuilt on a merge.
func (v Verdict) Check() error {
	if err := v.Change.Check(); err != nil {
		return err
	}
	for _, id := range []struct{ name, v string }{{"base commit", v.BaseCommit}, {"onto", v.Onto}, {"candidate commit", v.CandidateCommit}} {
		if id.v != "" && !IsObjectID(id.v) {
			return fmt.Errorf("verdict on %s: %s %q is not a commit id", v.Change.Label(), id.name, id.v)
		}
	}
	if v.Regenerate != "" && v.Gate == "" {
		return fmt.Errorf("verdict on %s names a regeneration but no gate", v.Change.Label())
	}
	if v.After != "" {
		if err := CheckID(v.After); err != nil {
			return fmt.Errorf("verdict on %s: after: %w", v.Change.Label(), err)
		}
	}
	switch v.Decision {
	case DecisionMerge:
		switch {
		case v.Code != "":
			return fmt.Errorf("merge verdict on %s carries code %q", v.Change.Label(), v.Code)
		case v.BaseCommit == "" || v.Onto == "" || v.CandidateCommit == "":
			return fmt.Errorf("merge verdict on %s names no base commit, onto or candidate commit", v.Change.Label())
		case !v.Method.Valid():
			return fmt.Errorf("merge verdict on %s: merge method %q", v.Change.Label(), v.Method)
		}
		return nil
	case DecisionMerged:
		if v.Code != "" {
			return fmt.Errorf("merged verdict on %s carries code %q", v.Change.Label(), v.Code)
		}
		return nil
	case DecisionKick, DecisionWait:
		if got := v.Code.decision(); got != v.Decision {
			return fmt.Errorf("%s verdict on %s carries code %q", v.Decision, v.Change.Label(), v.Code)
		}
		return nil
	}
	return fmt.Errorf("unknown decision %q", v.Decision)
}
