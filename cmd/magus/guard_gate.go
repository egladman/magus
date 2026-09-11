package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/types"
)

const (
	// gateRepeatWindow is how far back a repeat is counted. Long enough to cover a
	// working session, short enough that yesterday's runs say nothing about today's.
	gateRepeatWindow = 2 * time.Hour

	// gateRepeatMinRuns is how many runs inside the window before saying anything.
	// The second is the first that could have been a narrower target.
	gateRepeatMinRuns = 2

	// gateRepeatMinSpent is how much those runs must have COST before it is worth
	// mentioning. Frequency alone is not waste: a repeat gate over an unchanged tree
	// is mostly cache hits and finishes in seconds, which is the cache working. What
	// is worth a word is a gate that keeps genuinely re-running.
	gateRepeatMinSpent = 5 * time.Minute
)

// adviseRepeatGate reports what the CI gate has already cost in this window, or ""
// when it has not run enough to be worth saying.
//
// The gate is the most expensive target by construction, because it runs everything
// the diff reaches, and it is the one an iterating caller reaches for out of habit.
//
// It reports rather than refuses, and deliberately does NOT judge whether a given
// run was justified. Nothing available separates a wasteful repeat from a legitimate
// re-run after a fix: the tree is dirty at the same revision either way. What is
// checkable is the accumulating cost, and stating it is what changes behavior.
//
// Read from the RUN LOG, not from forecast history. The history is user-global and
// keyed by workspace-relative project path, so every worktree records its root under
// "." in one file. This machine runs dozens, so a sibling checkout's gate would be
// counted and reported as having run "here". The run log is per-workspace, one file
// per invocation, carrying the argv and real wall clock, so the count is a fact
// rather than a reconstruction from per-spell samples.
func adviseRepeatGate(runsDir string, now time.Time) (full, brief string) {
	runs, spent := recentGateRuns(runsDir, now)
	if runs < gateRepeatMinRuns || spent < gateRepeatMinSpent {
		return "", ""
	}
	return gateRepeatAdvice(runs, spent), gateRepeatBrief(runs, spent)
}

// workspaceRunsDir is where this workspace logs its invocations, or "" when there is
// none to read. The hook runs with the workspace as its working directory.
func workspaceRunsDir(cacheDir string) string {
	if cacheDir == "" {
		cacheDir = ".magus"
	}
	dir := filepath.Join(cacheDir, cache.RunsDir)
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	return dir
}

// recentGateRuns counts the gate's invocations inside the window and sums their wall
// clock.
//
// One log file is one invocation, so there is nothing to cluster and no way to count
// the same run twice. Candidates are filtered by file modification time before any
// file is opened, which keeps a directory of thousands cheap.
func recentGateRuns(runsDir string, now time.Time) (runs int, spent time.Duration) {
	if runsDir == "" {
		return 0, 0
	}
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return 0, 0
	}
	cutoff := now.Add(-gateRepeatWindow)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || info.ModTime().Before(cutoff) {
			continue
		}
		started, ok := readGateInvocation(filepath.Join(runsDir, e.Name()))
		if !ok || started.Before(cutoff) {
			continue
		}
		runs++
		// The log's last write is the run's end; a run still in flight measures as
		// however far it has got, which is the honest reading of "spent so far".
		if d := info.ModTime().Sub(started); d > 0 {
			spent += d
		}
	}
	return runs, spent
}

// readGateInvocation reports when an invocation started, and whether it was the CI
// gate, by reading only its first line: the started event, which carries the argv.
func readGateInvocation(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return time.Time{}, false
	}
	var ev journal.Event
	if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Kind != journal.KindStarted || ev.Command == nil {
		return time.Time{}, false
	}
	if !isGateCommand(ev.Command.Arguments) {
		return time.Time{}, false
	}
	return time.UnixMilli(ev.Ts), true
}

// commandRunsGate reports whether a shell line would invoke the CI gate.
//
// Through the parser rather than a pattern, for the reason parseGuardCommands exists:
// it resolves quoting, peels wrappers, and hands back each command a line would
// actually run, so `mise exec -- ./magus affected ci` reads the same as the bare form
// and a `ci` inside a quoted argument reads as neither.
func commandRunsGate(command string) bool {
	cmds, ok := parseGuardCommands(command)
	if !ok {
		return false
	}
	for _, c := range cmds {
		if c.Name == "magus" && isGateCommand(c.Args) {
			return true
		}
	}
	return false
}

// gateReadFlags are the flags that turn a gate invocation into a REPORT about it: the
// shard plan, the affected set, why a project is in it, the command that would run.
//
// None of them runs a target, so none of them is what either caller is asking about: the
// deny exists because seven workers ran the whole pipeline at once, and the advisory
// reports accumulated wall clock. Measured by the structural breadcrumb test, which is how
// this was found: `magus affected ci --plan` is the command magus itself serves after an
// affected listing, and the deny refused magus's own suggestion to every worker.
var gateReadFlags = []string{"--plan", "--impact", "--explain", "--dry-run"}

// isGateCommand reports whether an argv RUNS the CI gate.
//
// An argv, so there is no shell quoting left to peel and no way for a trailing
// `-run ci` argument to a test command to masquerade as the gate. The two callers
// both supply one: a recorded invocation's arguments, and a parsed command line.
func isGateCommand(args []string) bool {
	for i, a := range args {
		if a != "run" && a != "affected" {
			continue
		}
		if slices.ContainsFunc(args[i+1:], func(f string) bool { return slices.Contains(gateReadFlags, f) }) {
			return false
		}
		for _, rest := range args[i+1:] {
			// Everything past `--` belongs to the underlying tool, where a bare `ci`
			// is a test filter rather than this workspace's gate.
			if rest == "--" {
				return false
			}
			if rest == "" || rest[0] == '-' {
				continue
			}
			// Every bare word, not just the first: magus accepts flags before the
			// target, and a flag's VALUE is a bare word too (`--timeout 5m ci`).
			// ParseTarget strips the charm suffix and normalizes, so `ci:rw` and `CI`
			// both resolve; a project path or a duration simply does not match.
			if t, err := types.ParseTarget(rest); err == nil && t.Name == types.TargetCI {
				return true
			}
		}
		return false
	}
	return false
}

// gateRepeatAdvice names no target but the gate itself: a workspace calls its
// narrower targets whatever it likes, so the advisory points at the command that
// lists them rather than guessing.
func gateRepeatAdvice(runs int, spent time.Duration) string {
	return fmt.Sprintf(
		"magus workspace: the `%s` gate has run %d times in this workspace in the last %s, about %s of wall clock. It runs everything the diff reaches, so it is the most expensive target here.\n",
		types.TargetCI, runs, gateRepeatWindow, spent.Round(time.Second)) +
		"While you iterate, a narrower target answers the same question faster. `" + hint.LsTargets.With("<project>") + "` lists what this workspace calls them, and `" + hint.Affected.With("--plan") + "` shows what the gate would run.\n" +
		"Save the full gate for the commit."
}

// gateRepeatBrief is the repeat form, and it keeps the running cost rather than going
// quiet: the count and the wall clock are the whole argument, and they are the part that
// has changed since the caller last read the full text.
func gateRepeatBrief(runs int, spent time.Duration) string {
	return fmt.Sprintf("magus workspace: the `%s` gate has run %d times here in the last %s, about %s of wall clock. `%s` lists narrower targets.\n",
		types.TargetCI, runs, gateRepeatWindow, spent.Round(time.Second), hint.LsTargets.With("<project>"))
}

// denyLeaseScopedGate refuses the gate to a lease that was handed a narrower check, and
// returns "" for everybody else.
//
// The gate runs ONCE per branch, in the orchestrator's tree, after every unit lands. A
// delegated worker's `validation` is the narrow target it was assigned, and until this
// rule the field declared that and enforced nothing: seven workers each ran the whole
// pipeline concurrently on one machine because every brief ended with it (2026-09-09).
// Gate redundancy cannot catch that, since it keys on identical tree content and seven
// worktrees are seven trees.
//
// Same seatbelt contract gradeLeasedWrite documents. A caller naming no lease, a lease
// with no live row, an unreadable ledger, and a row that declared no validation all pass:
// the last of those is a boundary nobody wrote, not a narrow one. A person running their
// own gate names no lease and never reaches this rule.
func denyLeaseScopedGate(ctx context.Context, actingLease, command string) string {
	if actingLease == "" || !commandRunsGate(command) {
		return ""
	}
	me, ok := actingLiveLease(ctx, actingLease)
	if !ok || me.Validation == "" || validationNamesGate(me.Validation) {
		return ""
	}
	return fmt.Sprintf(
		"magus workspace: run `%s` instead, which is the check lease %s was assigned. The orchestrator gates once, in its own tree, after every unit lands.\n"+
			"`%s` runs the `%s` gate, and the validation field on lease %s's ledger row reads %q, which does not name it. "+leaseActorClause("widen that field"),
		me.Validation, me.ID, command, types.TargetCI, me.ID, me.Validation)
}

// actingLiveLease reads the acting lease's own live row, reporting none whenever the
// ledger cannot answer. An unreadable ledger and an id nobody declared are one silence
// here: both leave nothing to judge against, and a rule the guard cannot evaluate must
// not block a tool call.
func actingLiveLease(ctx context.Context, actingLease string) (types.Lease, bool) {
	if !types.ValidLeaseID(actingLease) {
		return types.Lease{}, false
	}
	location := hookActivityTrail(ctx)
	if location.base == "" {
		return types.Lease{}, false
	}
	leases, err := ledger.NewStore(ledger.Location{CacheDir: location.base, Root: location.workspace}).List()
	if err != nil {
		return types.Lease{}, false
	}
	return liveLease(liveLeases(leases), actingLease)
}

// leaseStanding is where the acting lease's row stands in the ledger: whether the ledger
// could be read at all, whether it declares the id, and the state it recorded.
//
// Three answers, not two. "The ledger says nothing" and "the ledger could not be read" are
// the difference between an id nobody declared and a rule the guard cannot evaluate, and
// only the first is the caller's mistake.
type leaseStanding struct {
	readable bool
	declared bool
	state    types.LeaseState
}

// terminal reports a row that is declared and has stopped running, so its rules are inert.
func (s leaseStanding) terminal() bool {
	return s.declared && !s.state.Live()
}

// actingLeaseStanding looks the acting lease up across EVERY row, terminal ones included.
//
// actingLiveLease above answers "is there a boundary to grade against", which is one
// silence for both misses. This one answers "does this id mean anything here", which is
// what separates a typo from a plan that has already finished, and the two cases want
// opposite verdicts (friction synthesis 2026-09-11, C3: a dead id looked exactly like a
// guarded session).
func actingLeaseStanding(ctx context.Context, actingLease string) leaseStanding {
	if !types.ValidLeaseID(actingLease) || actingLease == "" {
		return leaseStanding{}
	}
	location := hookActivityTrail(ctx)
	if location.base == "" {
		return leaseStanding{}
	}
	leases, err := ledger.NewStore(ledger.Location{CacheDir: location.base, Root: location.workspace}).List()
	if err != nil {
		return leaseStanding{}
	}
	for _, u := range leases {
		if u.ID == actingLease {
			return leaseStanding{readable: true, declared: true, state: u.State}
		}
	}
	return leaseStanding{readable: true}
}

// denyUndeclaredLease refuses to grade a call for an id this workspace's ledger does not
// carry, and returns "" whenever it does carry one or cannot say.
//
// An id that names no row bought SILENT un-enrolled treatment before this: every
// lease-scoped rule reads it, finds nothing, and passes, so a worker whose orchestrator
// typo'd the id ran unguarded and looked exactly like a guarded one. The verdict is an
// error rather than an advisory because the caller asserted a binding that does not
// exist, and everything downstream of that assertion is wrong.
//
// Not the INVALID-id case, which stays an advisory (see gradeLeasedWrite): an id magus
// cannot parse is one it cannot look up either, and blocking a tool call over unparsable
// metadata is the failure the fail-open contract is written against.
func denyUndeclaredLease(standing leaseStanding, actingLease string) string {
	if !standing.readable || standing.declared {
		return ""
	}
	return fmt.Sprintf("magus workspace: lease %s is not declared; run `%s` to see the plan.\n"+
		"Every lease-scoped rule reads that row, so a call naming a row this workspace's ledger does not carry is graded by nothing at all. That is the shape a typo'd id takes: an agent that believes it is inside a boundary, running outside every one.",
		actingLease, hint.Ledger.String())
}

// noticeTerminalLease says that a declared row has stopped running, or "" for a live one.
//
// The rules keyed on the row are inert from that moment (liveLeases drops it), which is
// correct and invisible: the verdicts look identical to a session nobody leased. One line
// per session is what makes the difference readable.
func noticeTerminalLease(standing leaseStanding, actingLease string) string {
	if !standing.terminal() {
		return ""
	}
	return fmt.Sprintf("magus workspace: lease %s is in state %s; its rules are inert.\n"+
		"A terminal row has no boundary left to grade against, so the lease-scoped denials are not running for this session. If work is still in flight under that id, the orchestrator owns reopening it.",
		actingLease, standing.state)
}

// denyLeaseScopedRebind refuses, under a bound lease, every command that would rewrite
// WHO the caller is or what its row says.
//
// The store refuses these too (registered_by is the structural half). This rule is the
// half that arrives BEFORE the call, carrying the reason: an agent that reads a denial
// naming the tool reaches for the tool, which is how two independent personas got here
// (friction synthesis 2026-09-11, C2). Being told first costs one verdict; finding out
// from a store error costs a turn and teaches nothing about why.
//
// A READ is untouched. `magus session lease` with no operand prints the binding, and
// `magus ledger ls` prints the plan; refusing those would deny a worker the ability to
// find out what it is bound to, which is the opposite of what this rule is for.
//
// Unbound callers are untouched too, for the reason every lease rule here gives: an
// orchestrator and a person at a terminal both name no lease, and they are the ones who
// write rows.
func denyLeaseScopedRebind(ctx context.Context, actingLease, command string) string {
	if actingLease == "" {
		return ""
	}
	cmds, ok := parseGuardCommands(command)
	if !ok {
		return ""
	}
	for _, c := range cmds {
		what := leaseRebind(c, func() (types.Lease, bool) { return actingLiveLease(ctx, actingLease) })
		if what == "" {
			continue
		}
		return fmt.Sprintf(
			"magus workspace: leave your own row alone. "+leaseActorClause("change a lease row")+"\n"+
				"`%s` would %s, and this checkout is bound to lease %s. An agent that can move the rows it is graded against is graded against a boundary nobody handed it from the next call on, which is the one thing the ledger exists to make visible.",
			command, what, actingLease)
	}
	return ""
}

// ledgerRegisterVerb is the CLI spelling of writing a row from the terminal. It is a
// literal rather than a hint.Command because no such subcommand exists yet: it is the
// shape the person-writes-rows proposal takes, and the rule is here so the channel cannot
// open unguarded. A hint.Command declared for it would fail TestCLICommandPathsResolve,
// which is the right complaint about a command nobody can run.
const ledgerRegisterVerb = "register"

// leaseRebind names what a parsed command would do to the ledger when it is one a bound
// caller may not do, or "" for everything else. me reads the acting lease's own row, and
// is called only on the paths that need it.
//
// The MCP form is graded here alongside the CLI ones because it is the SAME write through
// a different transport, and a rule that held on one channel would move the traffic rather
// than stop it.
func leaseRebind(c guardCommand, me func() (types.Lease, bool)) string {
	if c.Name == hint.ToolLedger.String() {
		return ledgerToolRebind(mcpParams(c.Args), me)
	}
	if path.Base(c.Name) != "magus" {
		return ""
	}
	words := magusWords(c.Args)
	if len(words) < 2 {
		return ""
	}
	switch {
	// The read form prints the binding and takes no operand; only the form carrying one
	// rebinds. Refusing the read would leave a worker unable to find out what it is bound
	// to, which is the opposite of what this rule is for.
	case words[0] == hint.SessionLease.Head() && words[1] == hint.SessionLease.Leaf() && len(words) > 2:
		return "bind this checkout to another lease"
	case words[0] == hint.LedgerAccept.Head() && words[1] == hint.LedgerAccept.Leaf():
		return "grade a lease row"
	case words[0] == hint.Ledger.Head() && words[1] == ledgerRegisterVerb:
		return "write a lease row"
	}
	return ""
}

// magusWords is the bare words of a magus argv, stopping at `--` because everything past
// it belongs to an underlying tool. Flags are skipped wherever they sit, since magus
// accepts them before and after the subcommand.
//
// A flag's VALUE is a bare word too (`--root /tmp/x ledger accept`), which this reads as a
// subcommand token and so does not match. That is the safe direction: the rule fails to
// fire rather than firing on a path that happened to end in a verb.
func magusWords(args []string) []string {
	var words []string
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "" || a[0] == '-' {
			continue
		}
		words = append(words, a)
	}
	return words
}

// ledgerToolRebind judges one call to the ledger tool against the row the caller is bound
// to, naming what the call would do when it is something a bound caller may not.
//
// Three carve-outs, and each is somebody else's job rather than a hole:
//
//   - `op=register` on the caller's OWN row records the base it landed on. That is the
//     worker's own procedure, demanded by the checkpoint denial in gradeAgainstOwnLease,
//     and denying it here would leave a worker unable to write anywhere at all.
//   - `op=put` on the caller's own row that only SHRINKS its owned paths gives the lane
//     back. It passes to the store, which owns whether a given shrink is legitimate; the
//     guard's job is the direction, and giving up a lane cannot widen a role.
//   - a read (`op=list`, and the default) is not a write.
//
// Shrinking is verbatim membership, not glob containment: every declaration in the call
// must already be one the row carries, and there must be fewer of them. A cleverer pattern
// that happens to cover less is not something this rule will try to prove.
func ledgerToolRebind(params map[string]string, me func() (types.Lease, bool)) string {
	op, id := params["op"], params["id"]
	if op == "clear" {
		return "drop every ledger row"
	}
	if op != "put" && op != "register" {
		return ""
	}
	row, bound := me()
	if !bound || id == "" || id != row.ID {
		return "write another lease's ledger row"
	}
	if op == "register" {
		return ""
	}
	if shrinksOwnedPaths(params, row) {
		return ""
	}
	return "rewrite its own ledger row"
}

// shrinksOwnedPaths reports whether a put changes nothing but the row's owned paths, and
// only by removing entries it already carries.
func shrinksOwnedPaths(params map[string]string, row types.Lease) bool {
	for _, key := range mcpJudgedParams {
		switch key {
		case "op", "id", "owned_paths":
		default:
			if _, present := params[key]; present {
				return false
			}
		}
	}
	proposed := strings.Split(params["owned_paths"], ",")
	if _, present := params["owned_paths"]; !present || len(proposed) >= len(row.OwnedPaths) {
		return false
	}
	for _, decl := range proposed {
		if !slices.Contains(row.OwnedPaths, strings.TrimSpace(decl)) {
			return false
		}
	}
	return true
}

// mcpParams reads back the parameters renderMCPCall wrote. The guard sees an MCP call as
// `<tool name> <key>=<value>...`, normalized by the envelope decoder.
func mcpParams(args []string) map[string]string {
	params := make(map[string]string, len(args))
	for _, a := range args {
		if key, value, ok := strings.Cut(a, "="); ok {
			params[key] = value
		}
	}
	return params
}

// validationNamesGate reports whether a lease's declared validation IS the gate, in which
// case the lease owns it and nothing is refused.
//
// Read as words rather than through parseGuardCommands: the field is a declaration a
// person wrote, and `ci`, `magus run ci` and `affected ci` are all things they write. A
// stray `ci` elsewhere in the field reads as ownership and clears the deny, which is the
// direction this rule fails in on purpose.
func validationNamesGate(validation string) bool {
	for _, word := range strings.Fields(validation) {
		if t, err := types.ParseTarget(word); err == nil && t.Name == types.TargetCI {
			return true
		}
	}
	return false
}

// denyLeaseScopedVCS refuses version-control mutation under a WORKER lease: a row with
// a parent. The orchestrator lands every unit from the worker's tree, so a worker that
// commits, pushes, stashes or reverts edits the state it is being integrated from, and a
// whole-tree revert destroys a sibling's uncommitted work. A root lease, a lease with no
// row, and no lease at all are untouched: a boundary nobody declared is not one of size
// zero, the same rule the gate and the write arms follow.
//
// The command is parsed before the ledger is read: every tool call under a bound lease
// reaches this rule, and most of them are not git.
func denyLeaseScopedVCS(ctx context.Context, actingLease, command string) string {
	if actingLease == "" {
		return ""
	}
	cmds, ok := parseGuardCommands(command)
	if !ok {
		return ""
	}
	for _, c := range cmds {
		op := vcsMutation(c)
		if op == "" {
			continue
		}
		me, ok := actingLiveLease(ctx, actingLease)
		if !ok || me.Parent == "" {
			return ""
		}
		return fmt.Sprintf(
			"magus workspace: leave version control to the orchestrator: report your worktree path and `git status --short`, and it lands the work from there.\n"+
				"`%s` runs `%s`, and lease %s is a worker under %s in this workspace's ledger. A worker that commits, pushes, stashes or reverts changes the tree the orchestrator integrates from, and a whole-tree revert destroys a sibling's uncommitted work. "+leaseActorClause("clear its parent"),
			command, op, me.ID, me.Parent)
	}
	return ""
}

// vcsMutation names the git operation a parsed command performs when it is one a
// worker must leave to the orchestrator, or "" for anything else. Global options
// before the subcommand (-C <dir>, -c k=v, --work-tree <dir>) are skipped so a relocated
// commit is still a commit. Only git is read: the guard's command grammar knows no other VCS, and a
// name here that promised more would be a rule nothing enforces.
//
// `git stash list` and `git stash show` read the stash rather than moving work onto
// it, so they pass; every other stash form is a mutation.
func vcsMutation(c guardCommand) string {
	if c.Name != "git" {
		return ""
	}
	var sub string
	var rest []string
	for i := 0; i < len(c.Args); i++ {
		a := c.Args[i]
		if strings.HasPrefix(a, "-") {
			switch a {
			case "-C", "-c", "--work-tree", "--git-dir", "--namespace":
				i++
			}
			continue
		}
		sub, rest = a, c.Args[i+1:]
		break
	}
	switch sub {
	case "commit", "push", "reset", "clean", "revert", "rebase", "merge", "cherry-pick":
		return "git " + sub
	case "stash":
		if len(rest) > 0 && (rest[0] == "list" || rest[0] == "show") {
			return ""
		}
		return "git stash"
	case "worktree":
		if slices.Contains(rest, "remove") {
			return "git worktree remove"
		}
	case "checkout", "restore":
		if slices.Contains(rest, ".") {
			return "git " + sub + " ."
		}
	}
	return ""
}
