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
// be resolved in time, which denies: the time is the part an agent can spend.
func askWorkspaceRules(ctx context.Context, seam functionSeam, loadFailure error, resolveApproved func(context.Context) (ruleCall, error), worktree ruleCall) rulesAnswer {
	var out rulesAnswer
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
	if resolveApproved != nil {
		resolveCtx, cancel := context.WithTimeout(ctx, approvedResolveTimeout)
		rule, err := resolveApproved(resolveCtx)
		expired := errors.Is(resolveCtx.Err(), context.DeadlineExceeded)
		cancel()
		switch {
		case err == nil && rule != nil:
			ask(decidedByApproved, rule)
		case expired:
			// "No rule" read under an expired deadline may be a read that failed, since the
			// approved state reports an absent file and a failed read alike.
			out.failures = append(out.failures, trail.RuleFailure{Side: decidedByApproved, Error: "resolving the rule took longer than " + approvedResolveTimeout.String()})
			out.answer = types.GuardVerdict{Decision: types.GuardDeny, Reason: approvedRuleTimedOut(seam)}
			out.by = decidedByApproved
		case err != nil:
			out.failures = append(out.failures, trail.RuleFailure{Side: decidedByApproved, Error: "the rule could not be resolved: " + err.Error()})
		}
	}
	ask(decidedByWorktree, worktree)
	return out
}

// applyWorkspaceAnswer merges a workspace rule's answer into the built-in verdict, which
// decided names the side of. Strengthen only: a deny replaces whatever stood, an advise
// fills a pass or is added to a built-in advice, and an allow changes nothing.
func applyWorkspaceAnswer(verdict Verdict, decided string, asked rulesAnswer, rule string) (Verdict, string) {
	answer := asked.answer
	switch {
	case answer.Decision == types.GuardDeny:
		return Verdict{SchemaVersion: verdict.SchemaVersion, Decision: "deny", Reason: answer.Reason, Rule: rule, Lease: verdict.Lease}, asked.by
	case answer.Decision == types.GuardAdvise && verdict.Decision == "pass":
		return Verdict{SchemaVersion: verdict.SchemaVersion, Decision: "advise", Context: answer.Reason, Rule: rule, Lease: verdict.Lease}, asked.by
	case answer.Decision == types.GuardAdvise:
		// The built-in advice keeps its rule; the workspace's is added, never dropped.
		verdict.Context += "\n\n" + answer.Reason
		return verdict, decided + decidedByJoin + asked.by
	}
	return verdict, decided
}

// applyRuleFailureNote adds the note that a workspace rule judged nothing to any verdict
// short of a deny, which explains itself.
func applyRuleFailureNote(verdict Verdict, note string, kind hint.MarkerKind) Verdict {
	switch {
	case note == "" || verdict.Decision == "deny" || verdict.Decision == "ask":
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
	if seam == seamCommand {
		return advisoryCommandRuleFailed
	}
	return advisorySpawnRuleFailed
}
