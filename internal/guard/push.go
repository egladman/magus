package guard

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"go/build"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"mvdan.cc/sh/v3/syntax"
)

// Refusing a push that no green gate covers.
//
// This is the one rule here that is a CONTRACT rather than a nudge, and the distinction
// is the reason it exists. The push advisory it replaces fired on every `git push`
// unconditionally: it never read the run log, so it told a caller who had just gated and a
// caller who had never gated exactly the same thing. A notice that cannot tell those two
// apart is not a reminder, it is a toll, and the measured response to a toll is to stop
// reading it.
//
// What makes a refusal legitimate here is that magus can PROVE the finding. The run log
// records, per invocation, the argv that started it, the per-target results, and the
// version string the binary was built from -- which carries the commit. So "no green `ci`
// has run at this commit" is a fact read off disk, not a suspicion. That is the line the
// design principles draw: refuse what magus can prove, advise everything else.

// gateCoverage is what the run log says about the gate at one commit.
//
// ONE value, not a set of booleans. The states are mutually exclusive, and the first
// draft encoded them as three flags, which made illegal combinations expressible
// (unknown-and-passed) and immediately produced the bug they invite: the zero value read
// as "no gate ran" when it meant "nothing was asked", and the rule denied every push in a
// fresh clone. A reader also cannot tell from a bool pair which combinations are real.
type gateCoverage int

const (
	// gateUnknown is the question not being ASKABLE: no run log to read, or no commit to
	// match against. The zero value, and it must never deny: it is the state of a fresh
	// clone, a host with no VCS, and every test fixture.
	gateUnknown gateCoverage = iota
	// gateAbsent is a readable run log holding no gate run at this commit.
	gateAbsent
	// gateFailed is a gate run at this commit that FINISHED and did not pass.
	gateFailed
	// gateIncomplete is a gate run at this commit that started and wrote no finished
	// event: still running, or killed. The log cannot tell those apart, since both are a
	// missing record, so this names the superset rather than claiming the one it cannot
	// prove. Asking the project lock would distinguish them, which is machinery
	// this rule does not need: neither is coverage.
	gateIncomplete
	// gatePassed is a gate run at this commit that finished with every target passing.
	gatePassed
	// gateWaived is no passing gate at this commit, and a person's waiver of this commit
	// alone (`magus job grant --commit`).
	gateWaived
	// gateProjectScoped is a passing gate at this commit over named projects that left an
	// affected project out, such as `magus run ci libs/cmdline`, and no branch verdict.
	gateProjectScoped
	// gateUnscoped is a passing gate at this commit recorded before gate records named
	// their scope, and no branch verdict. It reads as project scope.
	// compat: see sessions.GateScope.Unscoped.
	gateUnscoped
	// gateNarrow is no passing gate at this commit, and passing narrow runs
	// covering every file changed since the last one (narrowCoverageAt).
	gateNarrow
	// gateUnrun is a branch gate at this commit that passed everything this
	// host could run. What it could not run is owed to PR CI.
	gateUnrun
	// gateRemote is no local `ci` verdict at this commit, and a workspace that
	// declares the remote CI that runs it (ci.remote_gate). A failed local run
	// is never this.
	gateRemote
)

// gateCoverageAt reads the run log for gate invocations built from commit.
//
// COMMIT, not tree state. An exact tree match is available -- the checkpoint digest would
// give it -- and it is the wrong threshold: it invalidates on the first comment typo after
// a green gate, which is the case the cadence explicitly allows and the case a caller
// would route around the rule to get. A commit with no green gate at all is the failure
// worth refusing, and it is the one people actually ship.
//
// Empty commit, or no run log, reports nothing known: this rule stands down rather than
// refusing on an absence it cannot account for.
func gateCoverageAt(runsDir, commit string) gateCoverage {
	if runsDir == "" || !matchableRevision(commit) {
		return gateUnknown
	}
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return gateUnknown
	}
	out := gateAbsent
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch readGateOutcome(filepath.Join(runsDir, e.Name()), commit) {
		case gatePassed:
			return gatePassed
		// A failure outranks an incomplete run when both are present: it is the more
		// definite thing to tell the caller, and the message differs.
		case gateFailed:
			out = gateFailed
		case gateIncomplete:
			if out == gateAbsent || out == gateProjectScoped {
				out = gateIncomplete
			}
		case gateProjectScoped:
			if out == gateAbsent {
				out = gateProjectScoped
			}
		}
	}
	return out
}

// gateVerdictAt is the verdict the gate recorded for commit in the repository's session
// store, or gateUnknown when there is none to read.
//
// Consulted before the run log, because the store is keyed by the commit a gate ran AT,
// while a run log names only the commit its binary was BUILT from. A gate run by a binary
// one commit behind HEAD, which is every gate after a commit that was not rebuilt, reads
// as absent in the log while the redundancy check (MGS3010) refuses to run it again: two
// rules each blocking the only way past the other.
func gateVerdictAt(workspace, commit string) gateCoverage {
	cover, _ := readGateHistory(workspace).recordAt(commit)
	return cover
}

// readGateFold reads a store's gate verdicts. A test counts reads through it.
var readGateFold = sessions.ReadGateResults

// gateHistory is the store's gate verdicts, read once per push judgment.
// A fold lists and stats every invocation in the store.
type gateHistory struct {
	fold sessions.Fold
	// read is false when there was no store to read.
	read bool
}

func readGateHistory(workspace string) gateHistory {
	if workspace == "" {
		return gateHistory{}
	}
	dir, err := sessions.Dir(workspace)
	if err != nil {
		return gateHistory{}
	}
	fold, err := readGateFold(dir)
	if err != nil {
		return gateHistory{}
	}
	return gateHistory{fold: fold, read: true}
}

// gateReplayScanLimit bounds how many distinct gated commits a push is
// placed against, newest first.
const gateReplayScanLimit = 64

// passedBranchGates are the distinct commits with a passing branch `ci`,
// newest first, at most gateReplayScanLimit of them.
func (h gateHistory) passedBranchGates() []sessions.GateRecord {
	var out []sessions.GateRecord
	seen := map[string]bool{}
	for i := len(h.fold.Records) - 1; i >= 0 && len(out) < gateReplayScanLimit; i-- {
		rec := h.fold.Records[i]
		var g sessions.GateResult
		if rec.Kind != sessions.KindGateResult || json.Unmarshal(rec.Payload, &g) != nil {
			continue
		}
		if g.Target != string(types.TargetCI) || g.Outcome != sessions.OutcomePass || !g.Scope.CoversBranch() || !matchableRevision(g.Commit) || seen[g.Commit] {
			continue
		}
		seen[g.Commit] = true
		out = append(out, sessions.GateRecord{GateResult: g, At: time.UnixMilli(rec.Ts)})
	}
	return out
}

// recordAt is the verdict h holds for commit, with the record it read.
func (h gateHistory) recordAt(commit string) (gateCoverage, sessions.GateRecord) {
	if !h.read || !matchableRevision(commit) {
		return gateUnknown, sessions.GateRecord{}
	}
	rec, ok := sessions.GateAt(h.fold, commit, string(types.TargetCI))
	switch {
	case !ok:
		return gateUnknown, rec
	case rec.Outcome == sessions.OutcomeUnrun && rec.Scope.CoversBranch():
		return gateUnrun, rec
	case !rec.BranchVerdict() && rec.Scope.Unscoped():
		return gateUnscoped, rec
	case !rec.BranchVerdict():
		return gateProjectScoped, rec
	case rec.Outcome == sessions.OutcomePass:
		return gatePassed, rec
	default:
		return gateFailed, rec
	}
}

// gatePassStatus is the overall outcome a finished gate writes. Journal statuses are
// plain strings on the wire (see journal.Invocation.Status), so this names the one value
// that counts rather than comparing a literal at the call site.
const gatePassStatus = "pass"

// readGateOutcome reports whether one run log is a gate invocation built from commit that
// finished passing, and whether it was a gate invocation at this commit at all.
//
// Folded through journal.InvocationFromEvents rather than by reading the two lifecycle
// events here: that function already knows which event carries the command, the version
// and the status, and a second reading of the same stream is a second thing to keep true.
// It also supplies the interrupted case for free -- a run killed at the terminal writes no
// finished event, so Status stays empty and reads as not-green, which is correct.
//
// The file is bounded before it is parsed. A run log holds every line a build emitted, so
// the biggest here run to megabytes, and this is called once per log on a push.
func readGateOutcome(path, commit string) gateCoverage {
	f, err := os.Open(path)
	if err != nil {
		return gateUnknown
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), gateLogLineMax)
	var events []journal.Event
	for sc.Scan() {
		var ev journal.Event
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		// The started event is the first line, so a log that is not this commit's gate
		// is abandoned on line one instead of being read to the end.
		if ev.Kind == journal.KindStarted {
			if ev.Command == nil || !isGateCommand(ev.Command.Arguments) || !namesCommit(ev.MagusVersion, commit) {
				return gateUnknown
			}
			// The log names the argv, not the affected set, so only `affected ci` is known
			// to cover the branch; a `run ci` names its own selection.
			if !isAffectedGateCommand(ev.Command.Arguments) {
				return gateProjectScoped
			}
		}
		if ev.Kind == journal.KindStarted || ev.Kind == journal.KindFinished {
			events = append(events, ev)
		}
	}
	if len(events) == 0 {
		return gateUnknown
	}
	switch journal.InvocationFromEvents("", events).Status {
	case gatePassStatus:
		return gatePassed
	// No status at all means no finished event: the run was killed, or is still going.
	case "":
		return gateIncomplete
	default:
		return gateFailed
	}
}

// gateLogLineMax bounds one line of a run log. A build's captured output lands here, so a
// line can be long; past this it is not a journal event any reader here understands.
const gateLogLineMax = 4 << 20

// minRevisionPrefix is the shortest abbreviation a revision is matched by. git's describe
// never abbreviates below it, and hg, Sapling and jj abbreviate to 12.
const minRevisionPrefix = 7

// matchableRevision reports whether id is a content hash abbreviation long enough to match
// by prefix: git, hg and Sapling node hashes and jj commit ids are all hex. Anything else
// cannot be matched against a recorded gate, so the rule stands down on it rather than
// matching by accident: an hg local revision number like 42 prefixes every node that starts
// with those digits, and a jj change id is spelled in k-z.
func matchableRevision(id string) bool {
	if len(id) < minRevisionPrefix {
		return false
	}
	for _, r := range id {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// namesCommit reports whether a magus version string names this commit.
//
// `git describe` renders it as v0.4.3-97-g4a1c0cc84, optionally with -dirty, so the
// commit appears as a g-prefixed abbreviation. Compared by PREFIX in both directions
// because the two sides abbreviate to different lengths: the describe output picks its
// own, and the caller passes whatever `vcs.Commit().short` gave it.
func namesCommit(version, commit string) bool {
	if version == "" || !matchableRevision(commit) {
		return false
	}
	for _, field := range strings.Split(version, "-") {
		abbrev, ok := strings.CutPrefix(field, "g")
		if !ok || !matchableRevision(abbrev) {
			continue
		}
		if strings.HasPrefix(commit, abbrev) || strings.HasPrefix(abbrev, commit) {
			return true
		}
	}
	return false
}

// askUnrendered is the rationale of askUnrenderedDeny, beside the asking rule's own verdict.
const askUnrendered = "This hook predates approval prompts, so it cannot ask the person and the call is refused instead. " +
	"Merge what `magus describe harness` prints or refresh the hook template from the magus docs, then try again; or run it from your own terminal."

// askUnrenderedDeny refuses a call whose verdict would be ask when the caller did not
// declare that it renders one. Such a hook predates the decision, renders it as nothing,
// and its host reads nothing as allow, so the call is refused instead of run unasked.
// subject names the call as the ask would have, such as "pushing abc1234 ungated".
func askUnrenderedDeny(subject string) string {
	return fmt.Sprintf("this hook predates approval prompts and cannot ask about %s; merge what `magus describe harness` prints.", subject)
}

// gradePushWithoutGate is "deny" for a push nothing covers, whoever pushes, and
// "" otherwise. Asking the root let two pull requests out ungated. A worker's
// refusal also sends the commit to the orchestrator.
func gradePushWithoutGate(ev pushEvidence, lease string) (decision, reason string) {
	cover, commit := ev.cover, ev.commit
	var state string
	switch cover {
	// Unknown passes. This rule refuses on evidence or not at all: no run log, no VCS, or a
	// host magus cannot read the revision from all mean the question was never asked, and
	// a refusal there is one nobody can clear.
	case gateUnknown, gatePassed, gateWaived, gateNarrow, gateUnrun, gateRemote:
		return "", ""
	case gateAbsent:
		state = "no `" + string(types.TargetCI) + "` run is recorded at this commit"
	case gateFailed:
		state = "the `" + string(types.TargetCI) + "` run at this commit failed"
	case gateIncomplete:
		state = "the `" + string(types.TargetCI) + "` run at this commit never finished"
	case gateProjectScoped:
		state = "the `" + string(types.TargetCI) + "` run at this commit covered named projects, not every project the branch affects"
	case gateUnscoped:
		state = "the `" + string(types.TargetCI) + "` verdict at this commit was recorded before gate records named their scope, so it reads as project scope"
	}
	head := fmt.Sprintf("pushing %s, which no passing gate covers: %s.\n", commit, state)
	if cover == gateIncomplete {
		head += "That run is still going or it was killed.\n"
	}
	head += ev.narrow.shortfall()
	gate := fmt.Sprintf("Or run `%s`, which waits for the build budget itself, commit what it regenerates, and push again. CI runs the identical command on the identical tree, so a red push pays twice for one answer.",
		hint.Affected.With(string(types.TargetCI)))
	if lease != "" {
		return decisionDeny, fmt.Sprintf("%sThis session is bound to job %s, and workers do not publish: report the commit to the orchestrator that holds the branch.\n%s", head, lease, gate)
	}
	return decisionDeny, head + gate
}

// movedPushReason refuses a push that follows, on the same line, a command that moves the
// revision it publishes. The revision is read before the line runs, so no gate record can
// be matched to the commit that goes out. It is refused for every caller, worker or not:
// nobody can be asked about a commit that does not exist yet, and the next call grades the
// real HEAD.
const movedPushReason = "this line pushes after a command that changes what is pushed: run that command on its own, then push in a separate call.\n" +
	"A commit, merge, rebase, reset or the like moves the revision after the gate is matched to it; the separate push is graded at the real HEAD."

// pushEvidence is what the stores say about the revision a push publishes.
type pushEvidence struct {
	cover  gateCoverage
	commit string
	// moved is a line that moves the revision before it pushes; commit is
	// then "".
	moved bool
	// replay is the gate on the identical tree that covers a replayed commit.
	replay sessions.GateRecord
	// unrun is the gate at commit that could not run every target here.
	unrun  sessions.GateRecord
	waiver types.GateWaiver
	// remoteGate is the workspace's ci.remote_gate, read when no local verdict
	// covers the commit.
	remoteGate string
	// narrow is weighed only when nothing else covers the commit and no
	// gate failed there.
	narrow narrowCoverage
}

// admitted says why a passing push went out with no `ci` verdict at its
// commit, "" when one covers it or the push is refused.
func (ev pushEvidence) admitted() string {
	if ev.moved {
		return ""
	}
	switch {
	case ev.replay.Inv != "":
		return fmt.Sprintf("magus workspace: %s is covered by `%s` at %s on the identical tree (%s).", ev.commit, types.TargetCI, ev.replay.Commit, gateRunRef(ev.replay))
	case ev.cover == gateWaived:
		return waiverNote(ev.commit, ev.waiver)
	case ev.cover == gateNarrow:
		return ev.narrow.admission(ev.commit)
	case ev.cover == gateRemote:
		return fmt.Sprintf("magus workspace: no local `%s` at %s; %s runs it on the pull request (ci.remote_gate).", types.TargetCI, ev.commit, ev.remoteGate)
	case ev.cover == gateUnrun:
		return fmt.Sprintf("magus workspace: PR CI owes what `%s` at %s (%s) could not run here:\n  %s",
			types.TargetCI, ev.commit, gateRunRef(ev.unrun), strings.Join(ev.unrun.Unrun, "\n  "))
	}
	return ""
}

// phasePushCoverage runs inside builtin_rules, so the two overlap.
const phasePushCoverage = "push_coverage"

// pushCoverage is what the stores say about the revision the push on command
// publishes, read in the checkout it runs in. It is gateUnknown when the line
// relocates the push somewhere this cannot name.
func pushCoverage(ctx context.Context, deps Dependencies, hook location, command string, d Dialect, cwd string) pushEvidence {
	log := judgmentPhases(ctx)
	log.begin(phasePushCoverage)
	start := time.Now()
	defer func() { log.record(timedPhase(phasePushCoverage, time.Since(start), 0, false)) }()
	push, located := locatePush(command, d, cwd)
	if !located {
		return pushEvidence{}
	}
	at := hook
	if push.relocated {
		at = hookLocationAt(deps, push.dir)
	}
	ev := pushEvidence{commit: deps.revision(ctx, push.dir, push.rev)}
	gates := readGateHistory(at.workspace)
	var rec sessions.GateRecord
	ev.cover, rec = gates.recordAt(ev.commit)
	if ev.cover == gateUnrun {
		ev.unrun = rec
	}
	if ev.cover == gateUnknown {
		ev.cover = gateCoverageAt(workspaceRunsDir(at.cacheDir), ev.commit)
	}
	var lineage gateLineage
	// A replay (cherry-pick, rebase) mints new commits over a tree a gate already passed.
	if ev.cover != gatePassed {
		lineage = gateLineageAt(ctx, deps.VCS, cmp.Or(push.dir, at.workspace), ev.commit, gates)
		if lineage.replayed {
			ev.cover, ev.replay = gatePassed, lineage.replay
		}
	}
	if ev.cover != gatePassed && ev.cover != gateUnknown {
		if w, waived := gateWaiverAt(at, ev.commit); waived {
			ev.cover, ev.waiver = gateWaived, w
		}
	}
	// A failed gate here is a red verdict that no narrow pass outweighs.
	switch ev.cover {
	case gateAbsent, gateIncomplete, gateProjectScoped, gateUnscoped:
		ev.narrow = narrowCoverageAt(ctx, deps, at, cmp.Or(push.dir, at.workspace), ev.commit, lineage)
		if ev.narrow.covers() {
			ev.cover = gateNarrow
		}
	}
	switch ev.cover {
	case gateAbsent, gateIncomplete, gateProjectScoped, gateUnscoped:
		if cfg, err := config.LoadWorkspaceOnly(at.workspace); err == nil && cfg.CI.RemoteGate != "" {
			ev.cover, ev.remoteGate = gateRemote, cfg.CI.RemoteGate
		}
	}
	// Only a question that could be asked becomes a refusal: a fresh clone or a host with
	// no run log still has nothing to prove.
	if ev.cover != gateUnknown && revisionMovesBeforePush(command, d, cwd) {
		ev.commit, ev.moved = "", true
	}
	return ev
}

// revisionMovesBeforePush reports a push on command that runs after a command that changes
// the revision it publishes, whichever of `;`, `&&`, `||`, a newline or a subshell joins
// them. The revision is read before any of it runs, so a gate recorded at that HEAD says
// nothing about the commit the push sends.
func revisionMovesBeforePush(command string, d Dialect, cwd string) bool {
	calls, ok := locateCalls(command, d, cwd, func(c hint.Invocation) bool { return isPush(c) || movesRevision(c) }, false)
	if !ok {
		return false
	}
	moved := false
	for _, c := range calls {
		if !isPush(c.inv) {
			moved = true
		} else if moved {
			return true
		}
	}
	return false
}

// movesRevision reports a git, hg, sl or jj command that changes the revision a later
// push publishes: it records a commit, replays or joins history, or resets the branch.
func movesRevision(c hint.Invocation) bool {
	sub, _ := vcsSubcommand(c)
	return slices.Contains(revisionMovers[c.Name], sub)
}

// revisionMovers are, per backend, the subcommands movesRevision reports.
var revisionMovers = map[string][]string{
	"git": {"commit", "merge", "cherry-pick", "am", "rebase", "reset", "pull", "revert"},
	"hg":  hgRevisionMovers,
	"sl":  hgRevisionMovers,
	"jj":  {"commit", "describe", "desc", "new", "squash", "rebase", "split", "abandon", "absorb", "duplicate", "revert"},
}

var hgRevisionMovers = []string{"commit", "ci", "amend", "merge", "rebase", "graft", "import", "histedit", "backout", "fold", "split", "absorb"}

// pushSite is where a push runs and what it publishes.
type pushSite struct {
	// dir is the directory the push runs in: absolute when relocated, otherwise the
	// hook's own, "" for the process's working directory.
	dir       string
	relocated bool
	// rev is the source of the single refspec a git push names, "" for the checkout's
	// current revision.
	rev string
}

// locatePush reads where the first push on command runs, starting from cwd and following
// a cd earlier on the line and the backend's relocation flags (git -C, hg -R, jj -R).
// False when the line pushes nothing this can find, or when the directory is not knowable
// without running anything: a variable, a substitution, a cd whose success decides what
// runs next, or a relocation inside a wrapper's script.
func locatePush(command string, d Dialect, cwd string) (pushSite, bool) {
	site, _, located := locateFirst(command, d, cwd, isPush)
	return site, located
}

// locateCheckout is where the checkout a command rule is handed stands: the first push's,
// or on a line that pushes nothing, the first commit's. A push that is found but cannot be
// located stays unknown rather than falling back, since the push rules read that checkout.
func locateCheckout(command string, d Dialect, cwd string) (pushSite, bool) {
	site, found, located := locateFirst(command, d, cwd, isPush)
	if found {
		return site, located
	}
	site, _, located = locateFirst(command, d, cwd, recordsCommit)
	return pushSite{dir: site.dir, relocated: site.relocated}, located
}

// pushArgs are the words the call runs with, read through a wrapper too: env, exec or
// timeout change nothing about which branch a push publishes.
func (c locatedCall) pushArgs() []*syntax.Word {
	if c.args != nil {
		return c.args
	}
	return c.wrapped
}

// locateFirst locates the first invocation on command that match accepts. found reports
// one was seen, located that its directory is known without running anything.
func locateFirst(command string, d Dialect, cwd string, match func(hint.Invocation) bool) (site pushSite, found, located bool) {
	calls, ok := locateCalls(command, d, cwd, match, true)
	if !ok || len(calls) == 0 {
		return pushSite{}, false, false
	}
	c := calls[0]
	args := c.pushArgs()
	switch {
	case c.unfollowed:
		return pushSite{}, true, false
	case args == nil:
		if !c.at.known || vcsRelocates(c.inv.Name, c.inv.Args) {
			return pushSite{}, true, false
		}
		site = pushSite{dir: c.at.dir, relocated: c.at.moved}
	default:
		if site, ok = parsePush(c.inv.Name, args, c.at); !ok {
			return pushSite{}, true, false
		}
	}
	if site.relocated && !filepath.IsAbs(site.dir) {
		abs, err := filepath.Abs(site.dir)
		if err != nil {
			return pushSite{}, true, false
		}
		site.dir = abs
	}
	return site, true, true
}

// recordsCommit reports a command that records a commit message: git, hg and sl commit
// (hg and sl also spell it ci), and jj commit and describe (describe also spelled desc).
func recordsCommit(c hint.Invocation) bool {
	switch c.Name {
	case "git", "hg", "sl", "jj":
	default:
		return false
	}
	sub, _ := vcsSubcommand(c)
	switch c.Name {
	case "git":
		return sub == "commit"
	case "jj":
		return sub == "commit" || sub == "describe" || sub == "desc"
	}
	return sub == "commit" || sub == "ci"
}

// shellDir is the working directory a shell line has reached at one point in it.
type shellDir struct {
	dir   string
	moved bool
	known bool
}

// locatedCall is one invocation a callWalk matched, and the directory the shell had
// reached when it ran.
type locatedCall struct {
	inv hint.Invocation
	at  shellDir
	// program is the word that named the program, as typed: inv.Name is its
	// base. Empty for a call reached through a wrapper.
	program string
	// args are the call's words after the program, nil when the invocation was reached
	// through a wrapper, whose arguments arrive only as rendered text.
	args []*syntax.Word
	// wrapped are the words a wrapper handed the call, nil when none did. Apart from args
	// because the worktree and lease rules refuse a wrapped call outright.
	wrapped []*syntax.Word
	// unfollowed is set for a call inside a conditional, function or loop other than a
	// literalLoop, which the walk finds but does not locate.
	unfollowed bool
}

// locateCalls returns the invocations on command that match, in the order the shell
// runs them, each with the directory it runs in, starting from cwd. first stops at the
// first match. False when the line does not parse.
func locateCalls(command string, d Dialect, cwd string, match func(hint.Invocation) bool, first bool) ([]locatedCall, bool) {
	return locateCallsAt(command, d, shellDir{dir: cwd, known: true}, match, first)
}

// locateCallsAt is locateCalls for a line that starts at start.
func locateCallsAt(command string, d Dialect, start shellDir, match func(hint.Invocation) bool, first bool) ([]locatedCall, bool) {
	f, err := parseFile(command, d)
	if err != nil {
		return nil, false
	}
	w := callWalk{d: d, match: match, first: first, vars: map[string]string{}}
	// A function defined on the line may rebind a loop variable from inside the body.
	syntax.Walk(f, func(n syntax.Node) bool {
		_, fn := n.(*syntax.FuncDecl)
		w.funcs = w.funcs || fn
		return !w.funcs
	})
	w.stmts(f.Stmts, start)
	return w.calls, true
}

// callWalk follows a line's statements in the order the shell runs them, carrying the
// working directory, and records each matching invocation.
type callWalk struct {
	d     Dialect
	match func(hint.Invocation) bool
	first bool
	calls []locatedCall
	// vars binds each variable of the literal loops the walk is inside to this
	// iteration's word.
	vars  map[string]string
	funcs bool
}

func (w *callWalk) done() bool { return w.first && len(w.calls) > 0 }

func (w *callWalk) stmts(list []*syntax.Stmt, at shellDir) shellDir {
	for _, s := range list {
		if w.done() {
			break
		}
		at = w.stmt(s, at)
	}
	return at
}

func (w *callWalk) stmt(s *syntax.Stmt, at shellDir) shellDir {
	if s == nil || s.Cmd == nil || w.done() {
		return at
	}
	if s.Background {
		w.command(s.Cmd, at)
		return at
	}
	return w.command(s.Cmd, at)
}

func (w *callWalk) command(c syntax.Command, at shellDir) shellDir {
	switch c := c.(type) {
	case *syntax.CallExpr:
		return w.call(c, at)
	case *syntax.BinaryCmd:
		switch c.Op {
		case syntax.AndStmt:
			return w.stmt(c.Y, w.stmt(c.X, at))
		case syntax.OrStmt:
			// The right side runs only when the left failed, so a cd on the left leaves it,
			// and everything after, in a directory that depends on that failure.
			if after := w.stmt(c.X, at); after != at {
				at.known = false
			}
			w.stmt(c.Y, at)
			return at
		default:
			// Each side of a pipeline runs in its own subshell.
			w.stmt(c.X, at)
			w.stmt(c.Y, at)
			return at
		}
	case *syntax.Subshell:
		w.stmts(c.Stmts, at)
		return at
	case *syntax.TimeClause:
		// `time` is a keyword wrapping a statement, not a wrapper program.
		return w.stmt(c.Stmt, at)
	case *syntax.Block:
		return w.stmts(c.Stmts, at)
	case *syntax.ForClause:
		if name, words, ok := literalLoop(c); ok && !w.funcs {
			for _, word := range words {
				if w.done() {
					break
				}
				w.vars[name] = word
				at = w.stmts(c.Do, at)
			}
			// The shell leaves the variable set, but an unbound one only refuses more.
			delete(w.vars, name)
			return at
		}
	}
	// Conditionals, other loops and functions are not followed: a match inside one is found
	// but not located, and a cd inside one leaves the directory unknown.
	syntax.Walk(c, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok && !w.done() {
			for _, inv := range peelWrappers(literalWords(w.bound(call.Args)), w.d) {
				switch {
				case w.match(inv):
					w.calls = append(w.calls, locatedCall{inv: inv, at: at, unfollowed: true})
				case changesDir(inv):
					at.known = false
				}
			}
		}
		return !w.done()
	})
	return at
}

func (w *callWalk) call(c *syntax.CallExpr, at shellDir) shellDir {
	args := w.bound(c.Args)
	words := literalWords(args)
	if len(words) == 0 {
		return at
	}
	invs := peelWrappers(words, w.d)
	if len(invs) == 1 && invs[0].Name == path.Base(words[0]) {
		switch inv := invs[0]; {
		case isCdInvocation(inv):
			return cdInto(args[1:], at)
		case changesDir(inv):
			at.known = false
		case w.match(inv):
			w.calls = append(w.calls, locatedCall{inv: inv, at: at, program: words[0], args: args[1:]})
		}
		return at
	}
	for _, inv := range invs {
		switch {
		case changesDir(inv):
			at.known = false
		case w.match(inv):
			w.calls = append(w.calls, locatedCall{inv: inv, at: at, wrapped: wrappedArgs(args, words, inv)})
			if w.done() {
				return at
			}
		}
	}
	return at
}

// wrappedArgs are the words a wrapper hands inv, found as the run of words that spells it:
// env, exec, timeout and the like pass the program its own words unchanged, and find -exec
// passes the ones before its terminator. Nil when no run spells it, as for a `sh -c` script,
// whose words arrive only as rendered text.
func wrappedArgs(args []*syntax.Word, words []string, inv hint.Invocation) []*syntax.Word {
	for i := range words {
		end := i + 1 + len(inv.Args)
		if end <= len(words) && path.Base(words[i]) == inv.Name && slices.Equal(words[i+1:end], inv.Args) {
			return args[i+1 : end]
		}
	}
	return nil
}

// bound is words with each plain $NAME or ${NAME} of a bound loop variable replaced by its
// word, so the literal readers downstream see the value this iteration passes.
func (w *callWalk) bound(words []*syntax.Word) []*syntax.Word {
	if len(w.vars) == 0 {
		return words
	}
	out := make([]*syntax.Word, len(words))
	for i, word := range words {
		out[i] = &syntax.Word{Parts: w.boundParts(word.Parts)}
	}
	return out
}

func (w *callWalk) boundParts(parts []syntax.WordPart) []syntax.WordPart {
	out := make([]syntax.WordPart, len(parts))
	for i, part := range parts {
		out[i] = part
		switch p := part.(type) {
		case *syntax.ParamExp:
			if !plainParam(p) {
				continue
			}
			if v, ok := w.vars[p.Param.Value]; ok {
				out[i] = &syntax.Lit{Value: v}
			}
		case *syntax.DblQuoted:
			out[i] = &syntax.DblQuoted{Dollar: p.Dollar, Parts: w.boundParts(p.Parts)}
		}
	}
	return out
}

// plainParam reports $NAME or ${NAME} with no operator, whose value is the variable's own.
func plainParam(p *syntax.ParamExp) bool {
	return p.Param != nil && p.Flags == nil && !p.Excl && !p.Length && !p.Width && !p.IsSet &&
		p.NestedParam == nil && p.Index == nil && len(p.Modifiers) == 0 &&
		p.Slice == nil && p.Repl == nil && p.Names == 0 && p.Exp == nil
}

// literalLoop is the variable and words of a `for NAME in WORDS` loop the walk unrolls.
// False unless every word is literal and expands unchanged wherever the body reads it
// unquoted (no glob, brace, tilde, escape or blank), and nothing in the body can rebind
// NAME or skip the rest of an iteration.
func literalLoop(c *syntax.ForClause) (string, []string, bool) {
	iter, ok := c.Loop.(*syntax.WordIter)
	if !ok || c.Select || !iter.InPos.IsValid() {
		return "", nil, false
	}
	name := iter.Name.Value
	words := make([]string, 0, len(iter.Items))
	for _, item := range iter.Items {
		v, ok := literalArg(item.Parts)
		if !ok || v == "" || strings.ContainsAny(v, "*?[]{}~\\ \t\n") {
			return "", nil, false
		}
		words = append(words, v)
	}
	escapes := false
	for _, s := range c.Do {
		syntax.Walk(s, func(n syntax.Node) bool {
			switch n := n.(type) {
			case *syntax.Assign:
				escapes = n.Name != nil && n.Name.Value == name
			case *syntax.WordIter:
				escapes = n.Name.Value == name
			case *syntax.DeclClause, *syntax.LetClause, *syntax.ArithmCmd, *syntax.ArithmExp:
				escapes = true
			case *syntax.CallExpr:
				escapes = rebindsOrLeaves(literalWords(n.Args))
			}
			return !escapes
		})
		if escapes {
			return "", nil, false
		}
	}
	return name, words, true
}

// rebindsOrLeaves reports a builtin that can set a variable by name or cut an iteration
// short, after which the unrolled body no longer matches what the shell runs.
func rebindsOrLeaves(words []string) bool {
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "break", "continue", "read", "mapfile", "readarray", "getopts", "unset", "eval", "source", ".",
		"export", "declare", "typeset", "local", "readonly", "let":
		return true
	case "printf":
		return slices.ContainsFunc(words[1:], func(a string) bool { return strings.HasPrefix(a, "-v") })
	}
	return false
}

// changesDir reports a builtin that moves the shell's working directory.
func changesDir(c hint.Invocation) bool {
	return isCdInvocation(c) || c.Name == "pushd" || c.Name == "popd"
}

// cdInto is the directory after `cd` with args, unknown unless they name one literal path.
func cdInto(args []*syntax.Word, at shellDir) shellDir {
	var target string
	operands := 0
	for _, w := range args {
		lit, ok := literalArg(w.Parts)
		switch {
		case !ok:
			at.known = false
			return at
		case operands == 0 && (lit == "-L" || lit == "-P" || lit == "--"):
			continue
		}
		target = lit
		operands++
	}
	// No operand is $HOME, `-` is $OLDPWD, and `~` expands from the environment.
	if operands != 1 || target == "" || target == "-" || strings.HasPrefix(target, "~") {
		at.known = false
		return at
	}
	return changeDir(at, target)
}

// changeDir is at after changing into target. An absolute target is known wherever at was.
func changeDir(at shellDir, target string) shellDir {
	if filepath.IsAbs(target) {
		return shellDir{dir: filepath.Clean(target), moved: true, known: true}
	}
	at.dir, at.moved = filepath.Join(at.dir, target), true
	return at
}

// relocatingFlags are, per backend other than git, the options naming the checkout a
// command runs against, accepted anywhere on the line. git's is -C, read by parseGit.
var relocatingFlags = map[string][]string{
	"hg": {"-R", "--repository", "--repo", "--cwd"},
	"sl": {"-R", "--repository", "--repo", "--cwd"},
	"jj": {"-R", "--repository"},
}

// vcsRelocates reports whether rendered args carry a flag moving the program off its
// working directory.
func vcsRelocates(program string, args []string) bool {
	if program == "git" {
		g := parseGit(args)
		return len(g.dirs) > 0 || g.opaque
	}
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		if slices.Contains(relocatingFlags[program], name) {
			return true
		}
	}
	return false
}

// gitPushSite is parsePush for git: where its -C chain leads from at, and the revision the
// refspec names.
func gitPushSite(args []*syntax.Word, at shellDir) (pushSite, bool) {
	at, g, ok := gitCallAt(args, at)
	if !ok {
		return pushSite{}, false
	}
	return pushSite{dir: at.dir, relocated: at.moved, rev: pushedRev(args[g.at+1:])}, true
}

// gitCallAt is where a git call spelled with args runs, starting from at, and the literal
// prefix of its argv as git reads it. False when a word git reads before its subcommand is
// not literal, since a variable there may be the subcommand itself, or when --git-dir or
// --work-tree name the repository by a path no checkout can be read off.
func gitCallAt(args []*syntax.Word, at shellDir) (shellDir, gitCommand, bool) {
	lits := make([]string, 0, len(args))
	for _, w := range args {
		lit, ok := literalArg(w.Parts)
		if !ok {
			break
		}
		lits = append(lits, lit)
	}
	g := parseGit(lits)
	if g.at < 0 || g.opaque {
		return at, g, false
	}
	for _, dir := range g.dirs {
		if strings.HasPrefix(dir, "~") {
			return at, g, false
		}
		at = changeDir(at, dir)
	}
	return at, g, at.known
}

// parsePush is where a push spelled with args runs, starting from at, and the revision a
// git push publishes. False when a relocating operand is not one literal path.
func parsePush(program string, args []*syntax.Word, at shellDir) (pushSite, bool) {
	if program == "git" {
		return gitPushSite(args, at)
	}
	globals := vcsGlobals[program]
	moving := relocatingFlags[program]
	for i := 0; i < len(args); i++ {
		lit, ok := literalArg(args[i].Parts)
		if !ok {
			continue
		}
		name, value, joined := strings.Cut(lit, "=")
		if !strings.HasPrefix(lit, "-") {
			continue
		}
		if !joined && globals[name] {
			i++
			if i >= len(args) {
				return pushSite{}, false
			}
			value, ok = literalArg(args[i].Parts)
		}
		if !slices.Contains(moving, name) {
			continue
		}
		if !ok || value == "" || strings.HasPrefix(value, "~") {
			return pushSite{}, false
		}
		at = changeDir(at, value)
	}
	if !at.known {
		return pushSite{}, false
	}
	return pushSite{dir: at.dir, relocated: at.moved}, true
}

// pushedRev is the source of the one refspec a `git push` names, or "" for the checkout's
// current revision: no refspec or several, HEAD itself, a deletion, a whole-namespace
// push, or a word that is not literal.
func pushedRev(args []*syntax.Word) string {
	lits := make([]string, 0, len(args))
	for _, w := range args {
		lit, ok := literalArg(w.Parts)
		if !ok {
			return ""
		}
		lits = append(lits, lit)
	}
	for _, flag := range []string{"-d", "--delete", "--all", "--branches", "--mirror", "--tags"} {
		if slices.Contains(lits, flag) {
			return ""
		}
	}
	ops := operands(lits, "o")
	if len(ops) != 2 {
		return ""
	}
	src, _, _ := strings.Cut(strings.TrimPrefix(ops[1], "+"), ":")
	if src == "HEAD" || src == "@" || strings.HasPrefix(src, "-") {
		return ""
	}
	return src
}

// narrowCoverage is what passing runs short of the gate say about the files a
// push changes since its newest gated ancestor.
type narrowCoverage struct {
	// weighed is set once the changed files were read against the runs, and
	// unread says why they could not be.
	weighed bool
	unread  string
	// since is the newest ancestor with a passing `ci` when gated, else the
	// base ref the merge base is taken with.
	since     string
	gated     bool
	changed   []string
	hostless  []hostlessFile
	uncovered []uncoveredFile
}

// hostlessFile is a changed Go file this host does not build.
type hostlessFile struct{ path, builtOn string }

// uncoveredFile is a changed file no passing run covers, and runs that would.
type uncoveredFile struct {
	path string
	runs []string
}

func (n narrowCoverage) covers() bool {
	return n.weighed && n.unread == "" && len(n.uncovered) == 0
}

func (n narrowCoverage) from() string {
	if n.gated {
		return fmt.Sprintf("%s, the newest ancestor a passing `%s` covers", n.since, types.TargetCI)
	}
	return fmt.Sprintf("the merge base with %s, since no ancestor has a passing `%s`", n.since, types.TargetCI)
}

func (n narrowCoverage) admission(commit string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "magus workspace: %s goes out on narrow runs: a passing run of a target that reads it covers each of the %d files this host builds that changed since %s.",
		commit, len(n.changed)-len(n.hostless), n.from())
	for _, h := range n.hostless {
		fmt.Fprintf(&b, "\n%s: built only on %s; left to CI.", h.path, h.builtOn)
	}
	return b.String()
}

// shortfall is the refusal's account of the narrow runs, "" when none were
// weighed.
func (n narrowCoverage) shortfall() string {
	switch {
	case n.unread != "":
		return fmt.Sprintf("Narrow runs were not weighed: %s.\n", n.unread)
	case !n.weighed:
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "No passing run covers %d of the %d files changed since %s:\n", len(n.uncovered), len(n.changed), n.from())
	for _, u := range n.uncovered {
		if len(u.runs) == 0 {
			fmt.Fprintf(&b, "  %s: no target this workspace has run or names reads it, so only `%s` covers it\n", u.path, hint.Affected.With(string(types.TargetCI)))
			continue
		}
		fmt.Fprintf(&b, "  %s: run `%s`\n", u.path, strings.Join(u.runs, "` or `"))
	}
	for _, h := range n.hostless {
		fmt.Fprintf(&b, "  %s: built only on %s; left to CI\n", h.path, h.builtOn)
	}
	b.WriteString("Run those and push again.\n")
	return b.String()
}

// narrowCoverageAt weighs the passing runs the checkout's output store holds
// against each file commit changed since its newest gated ancestor, else since
// the merge base. dir is the checkout the push runs in, and lineage names
// that ancestor.
func narrowCoverageAt(ctx context.Context, deps Dependencies, at location, dir, commit string, lineage gateLineage) narrowCoverage {
	if deps.Inspect == nil || at.workspace == "" || at.cacheDir == "" || !matchableRevision(commit) {
		return narrowCoverage{unread: "this caller cannot open the workspace or its output store"}
	}
	res, err := vcs.Resolve(ctx, dir, "", deps.VCS)
	if err != nil || res.VCS == nil {
		return narrowCoverage{unread: "no version control answers what the push changes"}
	}
	root, err := res.VCS.Root(ctx, dir)
	if err != nil {
		return narrowCoverage{unread: fmt.Sprintf("the checkout root is unread: %v", err)}
	}
	n := narrowCoverage{since: res.Base}
	if lineage.descends {
		n.since, n.gated = lineage.ancestor, true
	}
	changed, err := res.VCS.RangeFiles(ctx, dir, n.since, commit, nil)
	if err != nil {
		return narrowCoverage{unread: fmt.Sprintf("the files changed since %s are unread: %v", n.since, err)}
	}
	ws, err := deps.inspect(ctx, at.workspace)
	if err != nil {
		return narrowCoverage{unread: fmt.Sprintf("the workspace did not open, so no target's inputs are known: %v", err)}
	}
	runs, err := cache.NewOutputStore(at.cacheDir).ListDescriptors()
	if err != nil {
		return narrowCoverage{unread: fmt.Sprintf("the output store is unread: %v", err)}
	}
	ev := runEvidence{
		driver: res.VCS, root: realPath(root), workspace: realPath(at.workspace), ws: ws, runs: runs, commit: commit,
		files: map[string]fileAt{}, revisions: map[string]bool{},
	}
	n.weighed, n.changed = true, changed
	for _, file := range changed {
		if on, ok := hostlessGo(filepath.Join(ev.root, filepath.FromSlash(file))); ok {
			n.hostless = append(n.hostless, hostlessFile{path: file, builtOn: on})
			continue
		}
		if !ev.covered(ctx, file) {
			n.uncovered = append(n.uncovered, uncoveredFile{path: file, runs: ev.wouldCover(file)})
		}
	}
	return n
}

// gateLineage places commit against the passing branch gates of a history.
type gateLineage struct {
	// replay is the newest gate on a tree equal to commit's.
	replay   sessions.GateRecord
	replayed bool
	// ancestor is the newest gated commit that commit descends from.
	ancestor string
	descends bool
}

// gateLineageAt places commit, in the checkout holding dir, against the
// passing branch gates h holds. An unread revision places nothing, so the
// push is graded by its commit alone.
func gateLineageAt(ctx context.Context, opts types.VCSOptions, dir, commit string, h gateHistory) gateLineage {
	gates := h.passedBranchGates()
	if dir == "" || !matchableRevision(commit) || len(gates) == 0 {
		return gateLineage{}
	}
	res, err := vcs.Resolve(ctx, dir, "", opts)
	if err != nil || res.VCS == nil {
		return gateLineage{}
	}
	if res.VCS.Name() == string(backendGit) {
		return gitGateLineage(ctx, dir, commit, gates)
	}
	return driverGateLineage(ctx, res.VCS, dir, commit, gates)
}

// gitGateLineage reads every gate's tree and ancestry in one `git rev-list`.
// Asking per gate cost two processes each, about a hundred on a busy store.
func gitGateLineage(ctx context.Context, dir, commit string, gates []sessions.GateRecord) gateLineage {
	revs := []string{commit}
	for _, g := range gates {
		revs = append(revs, g.Commit)
	}
	graph, err := gitCommitGraph(ctx, dir, revs)
	if err != nil {
		return gateLineage{}
	}
	head, ok := graph.resolve(commit)
	if !ok {
		return gateLineage{}
	}
	ancestors := graph.ancestors(head)
	var out gateLineage
	for _, g := range gates {
		id, ok := graph.resolve(g.Commit)
		if !ok {
			continue
		}
		if !out.replayed && graph.trees[id] == graph.trees[head] {
			out.replay, out.replayed = g, true
		}
		if !out.descends && ancestors[id] {
			out.ancestor, out.descends = g.Commit, true
		}
	}
	return out
}

// driverGateLineage is gitGateLineage for a backend without one batched
// read: one tree read and one ancestry check per gate.
func driverGateLineage(ctx context.Context, driver types.VCSDriver, dir, commit string, gates []sessions.GateRecord) gateLineage {
	tree, err := driver.TreeID(ctx, dir, commit)
	if err != nil || tree == "" {
		return gateLineage{}
	}
	var out gateLineage
	for _, g := range gates {
		if !out.replayed {
			if id, err := driver.TreeID(ctx, dir, g.Commit); err == nil && id == tree {
				out.replay, out.replayed = g, true
			}
		}
		if !out.descends {
			if ok, err := driver.IsAncestor(ctx, dir, g.Commit, commit); err == nil && ok {
				out.ancestor, out.descends = g.Commit, true
			}
		}
		if out.replayed && out.descends {
			break
		}
	}
	return out
}

// commitGraph is the commits reachable from some revisions: each one's tree
// and parents, keyed by full id.
type commitGraph struct {
	trees   map[string]string
	parents map[string][]string
}

// gitCommitGraph walks every commit reachable from revs in one process. A rev
// git cannot find, such as a gated commit since collected, is left out.
func gitCommitGraph(ctx context.Context, dir string, revs []string) (commitGraph, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-list", "--ignore-missing", "--stdin", "--format=%H %T %P")
	// Replacement refs would swap the trees compared.
	cmd.Env = append(os.Environ(), "GIT_NO_REPLACE_OBJECTS=1", "GIT_OPTIONAL_LOCKS=0")
	cmd.Stdin = strings.NewReader(strings.Join(revs, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		return commitGraph{}, fmt.Errorf("git rev-list: %w", err)
	}
	g := commitGraph{trees: map[string]string{}, parents: map[string][]string{}}
	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "commit" {
			continue
		}
		g.trees[fields[0]], g.parents[fields[0]] = fields[1], fields[2:]
	}
	return g, nil
}

// resolve is the one commit in g that rev abbreviates.
func (g commitGraph) resolve(rev string) (string, bool) {
	if _, ok := g.trees[rev]; ok {
		return rev, true
	}
	found := ""
	for id := range g.trees {
		if strings.HasPrefix(id, rev) {
			if found != "" {
				return "", false
			}
			found = id
		}
	}
	return found, found != ""
}

// ancestors is every commit in g that id descends from, id included.
func (g commitGraph) ancestors(id string) map[string]bool {
	seen := map[string]bool{id: true}
	for queue := []string{id}; len(queue) > 0; queue = queue[1:] {
		for _, p := range g.parents[queue[0]] {
			if !seen[p] {
				seen[p] = true
				queue = append(queue, p)
			}
		}
	}
	return seen
}

// runEvidence matches the checkout's recorded runs to the files a push changes.
type runEvidence struct {
	driver types.VCSDriver
	// root is the checkout's top level and workspace the workspace inside it,
	// both with symlinks resolved.
	root, workspace string
	ws              types.WorkspaceRepository
	runs            []cache.OutputDescriptor
	commit          string
	files           map[string]fileAt
	revisions       map[string]bool
}

// fileAt is one file at one revision; known is false when the revision is not
// in the repository.
type fileAt struct {
	body           string
	present, known bool
}

// covered reports a passing run that read file as commit holds it. A run
// whose target outputs hold file and wrote it so covers it too. file is
// repository-relative.
func (ev *runEvidence) covered(ctx context.Context, file string) bool {
	rel, inside := ev.workspacePath(file)
	if !inside {
		return false
	}
	for _, d := range ev.runs {
		if d.Failed {
			continue
		}
		if ev.reads(d, rel, true) && ev.heldAsCommit(ctx, file, d, false) || ev.writes(d, rel) && ev.heldAsCommit(ctx, file, d, true) {
			return true
		}
	}
	return false
}

// wouldCover names at most three runs that would cover file: targets this
// store ran over it, else magusfile targets whose inputs hold it.
func (ev *runEvidence) wouldCover(file string) []string {
	rel, inside := ev.workspacePath(file)
	if !inside {
		return nil
	}
	var out []string
	add := func(command string) {
		if len(out) < 3 && !slices.Contains(out, command) {
			out = append(out, command)
		}
	}
	for _, d := range ev.runs {
		if ev.writes(d, rel) {
			add(hint.Run.With(bareTarget(d.Target), d.Project))
			continue
		}
		if !ev.reads(d, rel, false) {
			continue
		}
		target := bareTarget(d.Target)
		if len(ev.namedPaths(d)) > 0 {
			add(hint.Run.With(target, d.Project, "--", projectRelative(d.Project, rel)))
		} else {
			add(hint.Run.With(target, d.Project))
		}
	}
	for _, p := range ev.ws.All() {
		for _, target := range p.MagusfileTargets {
			if target != string(types.TargetCI) && (footprintHolds(p, target, rel) || outputsHold(p, target, rel)) {
				add(hint.Run.With(target, p.Path))
			}
		}
	}
	return out
}

// workspacePath is file, repository-relative, relative to the workspace.
func (ev *runEvidence) workspacePath(file string) (string, bool) {
	prefix, err := filepath.Rel(ev.root, ev.workspace)
	if err != nil {
		return "", false
	}
	if prefix = filepath.ToSlash(prefix); prefix == "." {
		return file, true
	}
	return strings.CutPrefix(file, prefix+"/")
}

// reads reports whether run d's target declares rel among its inputs. scoped
// also holds a file-scoped run to the paths it named.
func (ev *runEvidence) reads(d cache.OutputDescriptor, rel string, scoped bool) bool {
	p := ev.ws.Get(d.Project)
	if p == nil || d.Target == "" || !footprintHolds(p, types.Normalize(bareTarget(d.Target)), rel) {
		return false
	}
	named := ev.namedPaths(d)
	if !scoped || len(named) == 0 {
		return true
	}
	return slices.ContainsFunc(named, func(n string) bool { return n == rel || strings.HasPrefix(rel, n+"/") })
}

// namedPaths are the workspace paths run d's forwarded args name, read
// against its project and then the workspace. None means the run is not
// file-scoped: `-run Push` names no path.
func (ev *runEvidence) namedPaths(d cache.OutputDescriptor) []string {
	var out []string
	for _, arg := range d.ExtraArgs {
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		for _, c := range []string{path.Join(d.Project, arg), path.Clean(arg)} {
			if !filepath.IsLocal(filepath.FromSlash(c)) {
				continue
			}
			if _, err := os.Lstat(filepath.Join(ev.workspace, filepath.FromSlash(c))); err == nil {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// writes reports whether run d's target declares rel among its outputs.
func (ev *runEvidence) writes(d cache.OutputDescriptor, rel string) bool {
	p := ev.ws.Get(d.Project)
	return p != nil && d.Target != "" && outputsHold(p, types.Normalize(bareTarget(d.Target)), rel)
}

// heldAsCommit reports whether run d read file (wrote it, when wrote is set)
// as commit holds it. A clean run is compared at its revision. A dirty run
// needs the tree to hold commit's file, unwritten since the run started, or
// for a writer since it finished.
func (ev *runEvidence) heldAsCommit(ctx context.Context, file string, d cache.OutputDescriptor, wrote bool) bool {
	want := ev.at(ctx, ev.commit, file)
	if !want.known {
		return false
	}
	if d.Revision != "" && !d.Dirty {
		return ev.at(ctx, d.Revision, file) == want
	}
	started := time.UnixMilli(d.TimestampMs - d.DurationMs)
	if wrote {
		started = time.UnixMilli(d.TimestampMs)
	}
	full := filepath.Join(ev.root, filepath.FromSlash(file))
	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		// A deletion dates only its directory.
		dir, err := os.Lstat(filepath.Dir(full))
		return !want.present && err == nil && !dir.ModTime().After(started)
	}
	if err != nil || !want.present || info.ModTime().After(started) {
		return false
	}
	body, err := os.ReadFile(full)
	return err == nil && string(body) == want.body
}

func (ev *runEvidence) at(ctx context.Context, rev, file string) fileAt {
	key := fmt.Sprintf("%s\x00%s", rev, file)
	if got, ok := ev.files[key]; ok {
		return got
	}
	known, asked := ev.revisions[rev]
	if !asked {
		_, err := ev.driver.FindCommit(ctx, ev.root, rev)
		known = err == nil
		ev.revisions[rev] = known
	}
	got := fileAt{}
	if known {
		// ReadFileAt errs on a path the revision lacks.
		body, err := ev.driver.ReadFileAt(ctx, ev.root, rev, file)
		got = fileAt{body: body, present: err == nil, known: true}
	}
	ev.files[key] = got
	return got
}

// footprintHolds reports whether target's source footprint in p holds rel. It
// copies job.targetSources, which internal/job keeps unexported.
func footprintHolds(p *types.Project, target, rel string) bool {
	if refs := p.TargetInputs[target]; len(refs) > 0 {
		return slices.ContainsFunc(refs, func(r types.InputRef) bool { return r.Rooted(p.Path).Match(rel) })
	}
	return slices.ContainsFunc(p.Sources, func(g types.Glob) bool { return g.Root(p.Path).Match(rel) })
}

// outputsHold reports whether target's own declared outputs in p hold rel.
// Project outputs do not count: any clean run would then cover them.
func outputsHold(p *types.Project, target, rel string) bool {
	return slices.ContainsFunc(p.TargetOutputs[target], func(r types.OutputRef) bool { return r.Rooted(p.Path).Match(rel) })
}

// bareTarget strips the charms a stored run's target carries (`lint:rw`).
func bareTarget(stored string) string {
	name, _, _ := strings.Cut(stored, ":")
	return name
}

// projectRelative is rel, workspace-relative, as project's args read it.
func projectRelative(project, rel string) string {
	if project == "." {
		return rel
	}
	return strings.TrimPrefix(rel, project+"/")
}

// hostlessGo reports a Go file this host's toolchain leaves out, by file name
// or build constraint, and the platforms that build it.
func hostlessGo(full string) (string, bool) {
	if !strings.HasSuffix(full, ".go") {
		return "", false
	}
	dir, name := filepath.Split(full)
	if built, err := build.Default.MatchFile(dir, name); err != nil || built {
		return "", false
	}
	var on []string
	for _, goos := range knownGOOS {
		c := build.Default
		c.GOOS = goos
		if built, err := c.MatchFile(dir, name); err == nil && built {
			on = append(on, goos)
		}
	}
	if len(on) == 0 {
		return fmt.Sprintf("a platform other than %s/%s", runtime.GOOS, runtime.GOARCH), true
	}
	return strings.Join(on, ", "), true
}

// knownGOOS are the operating systems hostlessGo names; go/build keeps its
// own list unexported.
var knownGOOS = []string{
	"aix", "android", "darwin", "dragonfly", "freebsd", "illumos", "ios", "js",
	"linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows",
}
