package guard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// ruleTimeout bounds one workspace rule call. The shipped host wiring kills a hook at ten
// seconds and reads that as a failed hook, so a rule that loops is cut off well inside it
// and fails open like any other broken rule.
const ruleTimeout = 3 * time.Second

// approvedResolveTimeout bounds resolving the approved rule: a VCS status, a read per
// source and a second load. An agent can slow the status (untracked files, say), so running
// out denies the call rather than dropping the side that would have judged it. With both
// rule calls it stays inside the host's ten seconds.
var approvedResolveTimeout = 3 * time.Second

// The sides a verdict can be decided by, recorded on the trail so a deny links to the
// policy that produced it.
const (
	decidedByBuiltin  = "builtin"
	decidedByWorktree = "worktree"
	// decidedByApproved is the approved sources' rule. HEAD is what approves today; the
	// name is the role, so a pinned approval replaces HEAD without renaming the record.
	decidedByApproved = "approved"
)

// decidedByJoin joins the sides that together reached a verdict, such as a built-in advice
// a workspace rule added to.
const decidedByJoin = "+"

// ruleCall is one side's workspace rule bound to the request it judges.
type ruleCall func(ctx context.Context) (types.GuardVerdict, error)

// functionSeam names one function-valued workspace rule for what the guard tells a reader.
type functionSeam string

const (
	seamSpawn   functionSeam = "spawn"
	seamCommand functionSeam = "command"
	seamWrite   functionSeam = "write"
)

func (s functionSeam) member() string { return `magus\guard.` + string(s) }

// rulesAnswer is what a workspace's function rule said about one call.
type rulesAnswer struct {
	answer types.GuardVerdict
	// by is the side whose answer stands, "" when none tightened anything.
	by string
	// answered are the sides whose rule ran and answered, in the order asked.
	answered []string
	// failures are the sides that judged nothing, and why.
	failures []trail.RuleFailure
	// timedOut is true when the answer is the deny for an approved side that did not
	// resolve in time; that deny already says so, so no failure note is added to it.
	timedOut bool
	// unloaded is true when the working tree failed to load and the approved side yielded
	// no rule either, so no policy was read at all. A side that loaded and then raised is
	// not unloaded: that stays fail-open.
	unloaded bool
	// loadFailure is why the working tree did not load, for denyUnloaded to tell a binary
	// older than the tree from a typo.
	loadFailure error
}

// askWorkspaceRules runs the approved rule and the working-tree rule and keeps the
// stricter answer. An uncommitted edit that tightens applies at once; one that loosens has
// no effect until it is approved.
//
// resolveApproved answers the approved side's rule, nil when it registers none; it is nil
// itself when the workspace has no approval authority wired. The approved side is asked on
// every call, since only it can say whether it has a rule: the working tree may have
// deleted its twin, or failed to load. It is asked first, so a working-tree rule that loops
// cannot spend the time it needs.
//
// A rule that fails contributes nothing and is reported, following magus\guard.shell's
// standing on a broken workspace rule: the built-ins still apply and the agent is not
// bricked by a typo in the magusfile. The one exception is an approved rule that could not
// be resolved in time, which denies: the time is the part an agent can spend. No side
// loading at all is reported as unloaded, for denyUnloaded to judge.
func askWorkspaceRules(ctx context.Context, seam functionSeam, loadFailure error, resolveApproved func(context.Context) (ruleCall, error), worktree ruleCall) rulesAnswer {
	out := rulesAnswer{loadFailure: loadFailure}
	if loadFailure != nil {
		out.failures = append(out.failures, trail.RuleFailure{Side: decidedByWorktree, Error: "the magusfile failed to load: " + loadFailure.Error()})
	}
	ask := func(by string, rule ruleCall) {
		if rule == nil || out.answer.Decision == types.GuardDeny {
			return
		}
		callCtx, cancel := context.WithTimeout(ctx, ruleTimeout)
		got, err := rule(callCtx)
		cancel()
		if err != nil {
			out.failures = append(out.failures, trail.RuleFailure{Side: by, Error: "the rule failed: " + err.Error()})
			return
		}
		out.answered = append(out.answered, by)
		merged := types.StricterGuardVerdict(out.answer, got)
		if merged.Decision != out.answer.Decision {
			out.by = by
		}
		out.answer = merged
	}
	approvedLoaded := false
	if resolveApproved != nil {
		resolveCtx, cancel := context.WithTimeout(ctx, approvedResolveTimeout)
		rule, err := resolveApproved(resolveCtx)
		expired := errors.Is(resolveCtx.Err(), context.DeadlineExceeded)
		cancel()
		switch {
		case err == nil && rule != nil:
			approvedLoaded = true
			ask(decidedByApproved, rule)
		case expired:
			// "No rule" read under an expired deadline may be a read that failed, since the
			// approved state reports an absent file and a failed read alike.
			out.failures = append(out.failures, trail.RuleFailure{Side: decidedByApproved, Error: "resolving the rule took longer than " + approvedResolveTimeout.String()})
			out.answer = types.GuardVerdict{Decision: types.GuardDeny, Reason: approvedRuleTimedOut(seam)}
			out.by = decidedByApproved
			out.timedOut = true
		case err != nil:
			out.failures = append(out.failures, trail.RuleFailure{Side: decidedByApproved, Error: "the rule could not be resolved: " + err.Error()})
		}
	}
	ask(decidedByWorktree, worktree)
	out.unloaded = loadFailure != nil && !approvedLoaded && worktree == nil
	return out
}

// unloadedCall is one call whose policy did not load, as the three seams describe it.
type unloadedCall struct {
	seam functionSeam
	// verb names, in backticks, the call the last loaded policy held back: a push, a merge,
	// a magus verb that writes shared state. "" for one the rule's absence may pass.
	verb string
	// changes is whether the call changes state: a file write, a spawn, or a shell line
	// that neither reads only nor is the fix.
	changes bool
	// what names the call in the deny.
	what string
	// lease is the acting lease, "" for the orchestrator or a person.
	lease string
}

// denyUnloaded turns asked into a deny when no side of the policy loaded, and reports
// whether it denied.
//
// Misconfiguration is an error: the rules that judge these calls are not running. Two
// cases deny, and every other call still passes on the built-ins, so the fix stays runnable.
//
// The binary judging the call cannot read the tree (older than it, or the checkout has no
// ./magus): every call that changes state is denied, and it needs no record of an earlier
// load, because a fresh worktree has none and is where this happens. The magusfile visibly
// registering a guard rule is enough.
//
// Otherwise a gated verb is denied when the last policy that did load registered seam's
// rule. No record means no rule was ever seen to protect, and the call passes.
func denyUnloaded(asked *rulesAnswer, call unloadedCall, at location) bool {
	if !asked.unloaded || asked.answer.Decision == types.GuardDeny {
		return false
	}
	own := ownSourceRoot(at.workspace)
	recorded := recordedRule(at.cacheDir, call.seam)
	cause := unloadCause(asked.loadFailure, at.workspace)
	var reason string
	switch {
	case call.changes && cause != "" && (recorded || declaresGuardRule(at.workspace)):
		reason = staleBinaryReason(call, cause, failureLines(asked.failures), own, at.workspace)
	case call.verb != "" && recorded:
		reason = unloadedReason(call, asked.failures, own, hasMagusBinary(at.workspace))
	default:
		return false
	}
	asked.answer = types.GuardVerdict{Decision: types.GuardDeny, Reason: reason}
	asked.by = decidedByBuiltin
	return true
}

// failureLines renders each failure as the side and the head of its error. An error
// annotated as stale carries the generic fix after a blank line, and the deny states the
// fix for the caller itself.
func failureLines(failures []trail.RuleFailure) []string {
	out := make([]string, len(failures))
	for i, f := range failures {
		head, _, _ := strings.Cut(f.Error, "\n\n")
		out[i] = f.Side + ": " + head
	}
	return out
}

// unloadedReason is the deny for a gated verb while the policy that registered seam's rule
// does not load. own is a checkout of magus itself, where the fix is a rebuild of ./magus.
func unloadedReason(call unloadedCall, failures []trail.RuleFailure, own, hasBinary bool) string {
	var b strings.Builder
	b.WriteString("magus workspace: " + call.verb + " is denied because this workspace's guard policy is not running. " +
		"It registered a " + call.seam.member() + " rule the last time it loaded, and now neither the working tree nor its approved copy loads:")
	for _, f := range failures {
		b.WriteString("\n  " + f.Side + ": " + f.Error)
	}
	b.WriteString("\nThat rule judges " + gatedCalls(call.seam) + ", so these wait until it loads; every other call still runs on the built-in rules.\n")
	if call.lease == "" && own && hasBinary {
		b.WriteString("The likeliest cause is a ./magus older than the tree. ")
	} else if call.lease == "" && !own {
		b.WriteString("The likeliest cause is a magus older than this workspace's magusfile, or an error in it. ")
	}
	b.WriteString(binaryRemedy(own, hasBinary, call.lease))
	return b.String()
}

// gatedCalls words what denyUnloaded holds back on seam.
func gatedCalls(seam functionSeam) string {
	if seam == seamSpawn {
		return "subagent spawns"
	}
	return "pushes, pull request merges and magus verbs that write shared state"
}

// applyWorkspaceAnswer merges a workspace rule's answer into the built-in verdict, which
// decided names the side of. Strengthen only: a deny replaces whatever stood, an advise
// fills a pass or is added to a built-in advice, and an allow changes nothing. An advise
// on a built-in ask is dropped, as every notice is on an ask.
func applyWorkspaceAnswer(verdict Verdict, decided string, asked rulesAnswer, rule string) (Verdict, string) {
	answer := asked.answer
	switch {
	case answer.Decision == types.GuardDeny:
		return Verdict{SchemaVersion: verdict.SchemaVersion, Decision: "deny", Reason: answer.Reason, Rule: rule, Lease: verdict.Lease}, asked.by
	case answer.Decision == types.GuardAdvise && verdict.Decision == "pass":
		return Verdict{SchemaVersion: verdict.SchemaVersion, Decision: "advise", Context: answer.Reason, Rule: rule, Lease: verdict.Lease}, asked.by
	case answer.Decision == types.GuardAdvise && verdict.Decision == "advise":
		// The built-in advice keeps its rule; the workspace's is added, never dropped.
		verdict.Context += "\n\n" + answer.Reason
		return verdict, decided + decidedByJoin + asked.by
	}
	return verdict, decided
}

// applyRuleFailureNote adds the note that a workspace rule judged nothing to every
// verdict: a deny or an ask reached with a rule missing must say it was judged short.
func applyRuleFailureNote(verdict Verdict, note string, kind hint.MarkerKind) Verdict {
	switch {
	case note == "":
	case verdict.Decision == "deny" || verdict.Decision == "ask":
		verdict.Reason += "\n\n" + note
	case verdict.Decision == "advise":
		verdict.Context += "\n\n" + note
	default:
		verdict.Decision, verdict.Context, verdict.Rule = "advise", note, string(kind)
	}
	return verdict
}

// approvedRuleTimedOut is the deny reason when the approved rule did not resolve in time.
func approvedRuleTimedOut(seam functionSeam) string {
	return "magus could not resolve the approved " + seam.member() + " rule in time, so this " + string(seam) +
		" is denied rather than judged without it. Resolving it runs a VCS status and reloads the committed " +
		"magusfile; retry once `git status` answers quickly in this checkout."
}

// sideName words one side of a seam for a reader.
func sideName(seam functionSeam, side string) string {
	switch side {
	case decidedByApproved:
		return "approved " + seam.member() + " rule"
	default:
		return "working tree's " + seam.member() + " rule"
	}
}

// ruleFailureNote tells the reader which rules judged this call when any workspace rule
// could not, "" when none failed. A set of failures is told in full the first time it fires
// in a session and as one line naming what applied on every repeat, so the reader never
// takes a call for judged by a rule that was skipped.
func ruleFailureNote(gate hint.Gate, seam functionSeam, failures []trail.RuleFailure, answered []string) string {
	if len(failures) == 0 {
		return ""
	}
	applied := []string{"the built-in rules"}
	for _, by := range answered {
		applied = append(applied, "the "+sideName(seam, by))
	}
	summary := "Only " + strings.Join(applied, " and ") + " applied to this " + string(seam) + "."
	var full strings.Builder
	h := sha256.New()
	for _, f := range failures {
		full.WriteString("The " + sideName(seam, f.Side) + " judged nothing: " + f.Error + "\n\n")
		_, _ = h.Write([]byte(f.Side + "\x00" + f.Error + "\x00"))
	}
	full.WriteString(summary)
	kind := hint.MarkerKind(string(ruleFailedKind(seam)) + "-" + hex.EncodeToString(h.Sum(nil)[:8]))
	return gate.OnceOrBrief(kind, full.String(), summary+" A workspace "+string(seam)+" rule is still failing, as told earlier in this session.")
}

// ruleFailedKind names the notice that a seam's workspace rule judged nothing.
func ruleFailedKind(seam functionSeam) hint.MarkerKind {
	switch seam {
	case seamCommand:
		return advisoryCommandRuleFailed
	case seamWrite:
		return advisoryWriteRuleFailed
	}
	return advisorySpawnRuleFailed
}
