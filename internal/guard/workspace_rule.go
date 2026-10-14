package guard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// The decisions a Verdict carries. A workspace rule answers advise or deny, spelled by
// types; pass and ask exist only on the wire.
const (
	decisionPass   = "pass"
	decisionAsk    = "ask"
	decisionAdvise = string(types.GuardAdvise)
	decisionDeny   = string(types.GuardDeny)
)

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
	// lapse is the first phase that ran out of its share of the hook's deadline, nil when
	// every phase answered in time. denyLapse decides what it costs the call.
	lapse *ruleLapse
	// approvedRuleless is true when the approved side resolved in time to no rule: nothing
	// pending differs from what the working tree loaded, the approved sources register
	// none, or there is no approval authority.
	approvedRuleless bool
	// dryRun is whether the judgment grades a call ahead of time, which words a lapse.
	dryRun bool
	// unloaded is true when the working tree failed to load and the approved side yielded
	// no rule either, so no policy was read at all. A side that loaded and then raised is
	// not unloaded: that stays fail-open.
	unloaded bool
	// loadFailure is why the working tree did not load, for denyUnloaded to tell a binary
	// older than the tree from a typo.
	loadFailure error
	// rule is the built-in rule denyUnloaded refused under, "" when the answer is the
	// workspace rule's own.
	rule denyRuleName
	// next is the command denyUnloaded's deny serves, nil for none.
	next []hint.Next
}

// resolveShares is the approved resolve's weight against a rule call's one. Resolving runs a
// VCS status and loads the committed magusfile a second time; a rule call is one function.
const resolveShares = 2

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
// Each phase (resolving the approved rule, calling it, calling the working tree's) gets its
// share of what remains of ctx's deadline, and runs to completion when ctx has none. One
// that runs out, a rule load deps reports lapsed, or a job store read that ran out
// (withJobStoreRows) is the answer's lapse, for denyLapse
// to judge. Any other failure contributes nothing and is reported, following
// magus\guard.shell's standing on a broken workspace rule: the built-ins still apply and
// the agent is not bricked by a typo in the magusfile. No side loading at all is reported
// as unloaded, for denyUnloaded to judge.
func askWorkspaceRules(ctx context.Context, seam functionSeam, deps Dependencies, resolveApproved func(context.Context) (ruleCall, error), worktree ruleCall) rulesAnswer {
	out := rulesAnswer{loadFailure: deps.LoadFailure, dryRun: isDryRun(ctx)}
	log := judgmentPhases(ctx)
	lapsed := func(l ruleLapse) {
		out.failures = append(out.failures, trail.RuleFailure{Side: l.side, Error: l.failure()})
		if out.lapse == nil {
			out.lapse = &l
		}
	}
	switch {
	case deps.RuleLoad.Lapsed:
		lapsed(ruleLapse{
			side: decidedByWorktree, budget: time.Duration(deps.RuleLoad.BudgetMs) * time.Millisecond,
			said: lapseWording{"loading this workspace's guard rules", "finish"}, failed: lapseWording{"the magusfile", "load"},
		})
	case deps.LoadFailure != nil:
		out.failures = append(out.failures, trail.RuleFailure{Side: decidedByWorktree, Error: "the magusfile failed to load: " + deps.LoadFailure.Error()})
	}
	// The lease rules read the job store, and one that could not be read in time judged
	// nothing.
	if p, ok := log.lapsedPhase(phaseJobRows); ok {
		lapsed(ruleLapse{
			side: decidedByBuiltin, budget: time.Duration(p.BudgetMs) * time.Millisecond,
			said: lapseWording{"reading the job store", "finish"}, failed: lapseWording{"the job store", "answer"},
		})
	}
	shares := phaseShares{}
	if resolveApproved != nil {
		shares[phaseApprovedResolve] = resolveShares
		shares[phaseApprovedRule] = 1
	}
	if worktree != nil {
		shares[phaseWorktreeRule] = 1
	}
	ask := func(by, phase string, rule ruleCall) {
		log.begin(phase)
		budget, bounded := shares.take(ctx, phase)
		if out.answer.Decision == types.GuardDeny {
			return
		}
		lapse := ruleLapse{side: by, budget: budget, said: lapseWording{"the " + sideName(seam, by), "answer"}, failed: lapseWording{"the rule", "answer"}}
		if bounded && budget == 0 {
			lapsed(lapse)
			return
		}
		callCtx, cancel := phaseContext(ctx, budget, bounded)
		start := time.Now()
		got, err := rule(callCtx)
		expired := err != nil && errors.Is(callCtx.Err(), context.DeadlineExceeded)
		cancel()
		log.record(timedPhase(phase, time.Since(start), budget, expired))
		switch {
		case expired:
			lapsed(lapse)
			return
		case err != nil:
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
		log.begin(phaseApprovedResolve)
		budget, bounded := shares.take(ctx, phaseApprovedResolve)
		var rule ruleCall
		var err error
		expired := bounded && budget == 0
		if !expired {
			resolveCtx, cancel := phaseContext(ctx, budget, bounded)
			start := time.Now()
			rule, err = resolveApproved(resolveCtx)
			expired = errors.Is(resolveCtx.Err(), context.DeadlineExceeded)
			cancel()
			log.record(timedPhase(phaseApprovedResolve, time.Since(start), budget, expired && (err != nil || rule == nil)))
		}
		out.approvedRuleless = err == nil && rule == nil && !expired
		if err == nil && rule != nil {
			approvedLoaded = true
			ask(decidedByApproved, phaseApprovedRule, rule)
		} else {
			delete(shares, phaseApprovedRule)
			switch {
			case expired:
				// "No rule" read under an expired deadline may be a read that failed, since
				// the approved state reports an absent file and a failed read alike.
				lapsed(ruleLapse{
					side: decidedByApproved, budget: budget,
					said:   lapseWording{"resolving the " + sideName(seam, decidedByApproved), "finish"},
					failed: lapseWording{"resolving the rule", "finish"},
				})
			case err != nil:
				out.failures = append(out.failures, trail.RuleFailure{Side: decidedByApproved, Error: "the rule could not be resolved: " + err.Error()})
			}
		}
	}
	if worktree != nil {
		ask(decidedByWorktree, phaseWorktreeRule, worktree)
	}
	out.unloaded = deps.LoadFailure != nil && !approvedLoaded && worktree == nil
	return out
}

// phaseContext bounds ctx to budget for a bounded phase and leaves it as it is otherwise.
func phaseContext(ctx context.Context, budget time.Duration, bounded bool) (context.Context, context.CancelFunc) {
	if !bounded {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, budget)
}

// approvedAnswered reports whether the approved side's rule ran and answered, which also
// means none of its phases ran out of time.
func (a rulesAnswer) approvedAnswered() bool {
	return slices.Contains(a.answered, decidedByApproved)
}

// denyShort turns a into a deny when its judgment fell short of what call needs: a phase
// ran out of time (denyLapse) or no side of the policy loaded (denyUnloaded). call is
// built only then, so the pass path pays nothing for it. Reports whether it denied; either
// deny names the failures itself.
func (a *rulesAnswer) denyShort(call func() judgedCall, at location) bool {
	switch {
	case a.lapse != nil:
		return denyLapse(a, call())
	case a.unloaded:
		return denyUnloaded(a, call(), at)
	}
	return false
}

// denyLapse turns asked into a deny when a phase of the judgment ran out of time and the
// call changes state, and reports whether it denied. A call that only reads stands on what
// did answer, with the failure note naming the phase that ran out.
func denyLapse(asked *rulesAnswer, call judgedCall) bool {
	if asked.lapse == nil || !call.changes || asked.answer.Decision == types.GuardDeny {
		return false
	}
	asked.answer = types.GuardVerdict{Decision: types.GuardDeny, Reason: lapseReason(call, *asked.lapse, asked.dryRun)}
	asked.by = asked.lapse.side
	return true
}

// judgedCall is one call whose policy did not load, as the three seams describe it.
type judgedCall struct {
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
func denyUnloaded(asked *rulesAnswer, call judgedCall, at location) bool {
	if !asked.unloaded || asked.answer.Decision == types.GuardDeny {
		return false
	}
	own := ownSourceRoot(at.workspace)
	recorded := recordedRule(at.cacheDir, call.seam)
	reason := unloadCause(asked.loadFailure, at.workspace)
	var d denial
	switch {
	case call.changes && reason.Cause != unloadNone && (recorded || declaresGuardRule(at.workspace)):
		d, asked.rule = staleBinaryDenial(call, reason, failureLines(asked.failures), own, at.workspace), denyRuleStaleBinary
	case call.verb != "" && recorded:
		d, asked.rule = unloadedDenial(call, asked.failures, own, hasMagusBinary(at.workspace)), denyRulePolicyUnloaded
	default:
		return false
	}
	// One string, verdict first: verdictParts splits it again when the deny is worded.
	asked.answer = types.GuardVerdict{Decision: types.GuardDeny, Reason: d.full()}
	asked.next = binaryRemedy(own, hasMagusBinary(at.workspace), call.lease).next(asked.rule)
	asked.by = decidedByBuiltin
	return true
}

// unknownSpellImport is the refusal of an import naming a spell this build does not ship,
// as the spell bindings raise it.
type unknownSpellImport interface{ UnknownSpell() string }

// skewedSpell names the spell loadFailure says this build does not ship when root, a
// checkout of magus itself, carries that spell's sources: the judging ./magus was built from
// a commit on the other side of the spell's addition or removal. "" otherwise. Outside
// magus's own checkout nothing tells a spell from a newer or older release apart from a
// misspelled import, so that stays the typo it may be.
func skewedSpell(loadFailure error, root string) string {
	if loadFailure == nil || root == "" || !ownSourceRoot(root) {
		return ""
	}
	var unknown unknownSpellImport
	if !errors.As(loadFailure, &unknown) {
		return ""
	}
	name := unknown.UnknownSpell()
	if info, err := os.Stat(filepath.Join(root, "spells", name)); err != nil || !info.IsDir() {
		return ""
	}
	return name
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

// unloadedDenial is the deny for a gated verb while the policy that registered seam's rule
// does not load. own is a checkout of magus itself, where the fix is a rebuild of ./magus.
func unloadedDenial(call judgedCall, failures []trail.RuleFailure, own, hasBinary bool) denial {
	fix := binaryRemedy(own, hasBinary, call.lease)
	var why strings.Builder
	why.WriteString("This workspace registered a " + call.seam.member() + " rule the last time it loaded, and now neither the working tree nor its approved copy loads:")
	for _, f := range failures {
		why.WriteString("\n  " + f.Side + ": " + f.Error)
	}
	why.WriteString("\nThat rule judges " + gatedCalls(call.seam) + ", so these wait until it loads; every other call still runs on the built-in rules.")
	switch {
	case call.lease == "" && own && hasBinary:
		why.WriteString("\nThe likeliest cause is a ./magus older than the tree.")
	case call.lease == "" && !own:
		why.WriteString("\nThe likeliest cause is a magus older than this workspace's magusfile, or an error in it.")
	}
	if fix.why != "" {
		why.WriteString("\n" + fix.why)
	}
	return denial{Say: call.verb + " waits for this workspace's guard policy to load; " + fix.say + ".", Why: why.String()}
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
// on a built-in ask is dropped, as every notice is on an ask. A deny denyUnloaded made is
// filed under its own built-in rule, with its next left unserved for the caller's shapeDeny
// to grade.
//
// seamRule is what an unnamed answer reports under; a named one reports as
// workspace:<name>, the id magus\guard.shell rules already carry.
func applyWorkspaceAnswer(verdict Verdict, decided string, asked rulesAnswer, seamRule string) (Verdict, string) {
	answer := asked.answer
	rule := seamRule
	switch {
	case asked.rule != "":
		rule = string(asked.rule)
	case answer.Rule != "":
		rule = workspaceShellPrefix + answer.Rule
	}
	switch {
	case answer.Decision == types.GuardDeny:
		return Verdict{SchemaVersion: verdict.SchemaVersion, Decision: decisionDeny, Reason: answer.Reason, Rule: rule, Lease: verdict.Lease, Next: asked.next}, asked.by
	case answer.Decision == types.GuardAdvise && verdict.Decision == decisionPass:
		return Verdict{SchemaVersion: verdict.SchemaVersion, Decision: decisionAdvise, Context: answer.Reason, Rule: rule, Lease: verdict.Lease}, asked.by
	case answer.Decision == types.GuardAdvise && verdict.Decision == decisionAdvise:
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
	case verdict.Decision == decisionDeny || verdict.Decision == decisionAsk:
		verdict.Reason += "\n\n" + note
	case verdict.Decision == decisionAdvise:
		verdict.Context += "\n\n" + note
	default:
		verdict.Decision, verdict.Context, verdict.Rule = decisionAdvise, note, string(kind)
	}
	return verdict
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

// ruleFailureReport is what ruleFailureNote tells: the rules of one seam that could not judge a
// call, and the ones that did.
type ruleFailureReport struct {
	gate     hint.Gate
	seam     functionSeam
	failures []trail.RuleFailure
	answered []string
	// toldInFull tells the failure set in full on every call rather than once a session: a
	// set holding a phase that ran out of time, since which phase ran out, and of how much,
	// is news each time.
	toldInFull bool
	// judging builds the line naming the magus that judged the call. It is read only when a
	// rule failed, since it stats the checkout.
	judging func() string
	// lead names a failure outside either policy copy, which must be repeated while a rule
	// is degraded so the reader knows how to repair it.
	lead string
}

// ruleFailureNote tells the reader which rules judged this call when any workspace rule
// could not, "" when none failed. A set of failures is told in full the first time it fires
// in a session (every time when a phase ran out of time) and as one line naming what
// applied on every repeat, so the reader never takes a call for judged by a rule that was
// skipped. The judging binary's line is part of the full telling only: the repeat stays one
// line. A non-empty lead opens both forms, since it names a cause the reader must act on.
func ruleFailureNote(r ruleFailureReport) string {
	if len(r.failures) == 0 {
		return ""
	}
	lead := r.lead
	if lead != "" {
		lead += "\n\n"
	}
	applied := []string{"the built-in rules"}
	for _, by := range r.answered {
		applied = append(applied, "the "+sideName(r.seam, by))
	}
	summary := "Only " + strings.Join(applied, " and ") + " applied to this " + string(r.seam) + "."
	var full strings.Builder
	full.WriteString(lead)
	h := sha256.New()
	for _, f := range r.failures {
		full.WriteString("The " + sideName(r.seam, f.Side) + " judged nothing: " + f.Error + "\n\n")
		_, _ = h.Write([]byte(f.Side + "\x00" + f.Error + "\x00"))
	}
	full.WriteString(summary)
	if r.judging != nil {
		if judged := r.judging(); judged != "" {
			full.WriteString("\n\n" + judged)
		}
	}
	if r.toldInFull {
		return full.String()
	}
	kind := hint.MarkerKind(string(ruleFailedKind(r.seam)) + "-" + hex.EncodeToString(h.Sum(nil)[:8]))
	return r.gate.OnceOrBrief(kind, full.String(), lead+summary+" A workspace "+string(r.seam)+" rule is still failing, as told earlier in this session.")
}

// judgingBinary is what is known about the magus that judged a call, told by String.
type judgingBinary struct {
	path, version string
	// root is the checkout's workspace root, "" when the call is outside one.
	root      string
	hasBinary bool
	// same reports that path is the checkout's own ./magus.
	same bool
	// unload is why the binary cannot load the tree, stale or a skewed spell; fix is then
	// the remedy to name. A missing ./magus is hasBinary's to tell.
	unload unloadReason
	fix    string
}

// judgingBinaryNote names the magus that judged the call and how it relates to the
// checkout's own ./magus, with the fix when the load failure is the binary being older than
// the tree. "" when the judge's path is unknown, which a caller outside the CLI leaves unset.
func judgingBinaryNote(deps Dependencies, at location, loadFailure error, lease string) string {
	if deps.Binary == "" {
		return ""
	}
	j := judgingBinary{path: deps.Binary, version: deps.BinaryVersion, root: at.workspace}
	if j.root != "" {
		j.hasBinary = hasMagusBinary(j.root)
		if j.hasBinary {
			a, errA := os.Stat(deps.Binary)
			b, errB := os.Stat(filepath.Join(j.root, "magus"))
			j.same = errA == nil && errB == nil && os.SameFile(a, b)
		}
		j.unload = unloadCause(loadFailure, j.root)
		if j.unload.Cause == unloadNoBinary {
			j.unload.Cause = unloadNone
			if j.unload.Spell != "" {
				j.unload.Cause = unloadSkewedSpell
			}
		}
		switch j.unload.Cause {
		case unloadStale:
			j.fix = binaryRemedy(ownSourceRoot(j.root), j.hasBinary, lease).say
		case unloadSkewedSpell:
			j.fix = binaryRemedy(true, j.hasBinary, lease).say
		}
	}
	return j.String()
}

func (j judgingBinary) String() string {
	who := "Judged by " + j.path
	if j.version != "" {
		who += " (" + j.version + ")"
	}
	if j.root == "" {
		return who + "."
	}
	switch {
	case !j.hasBinary:
		who += "; this checkout has no ./magus."
	case j.same:
		who += ", this checkout's ./magus."
	default:
		who += ", which is not this checkout's ./magus."
	}
	switch {
	case j.unload.Cause == unloadSkewedSpell:
		return who + " It ships no spell " + strconv.Quote(j.unload.Spell) + ", which the tree carries, so it was built from another commit. Fix: " + j.fix + "."
	case j.unload.Cause != unloadStale:
		return who
	case j.hasBinary && !j.same:
		return who + " It is older than the tree: the hook uses ./magus when the call runs inside this checkout and ./magus is executable. Otherwise " + j.fix + "."
	}
	return who + " Fix: " + j.fix + "."
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
