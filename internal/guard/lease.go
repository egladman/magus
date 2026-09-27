package guard

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/cli"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/json"
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

// workspaceRunsDir is where this workspace logs its invocations, or "" when there is none
// to read.
//
// An unresolved cache dir answers "" rather than falling back to the literal name: that
// name is relative, and joining it against the hook PROCESS's directory is the
// cross-checkout mistake hookLocationAt exists to prevent. No runs dir means no
// count, which is the honest answer.
func workspaceRunsDir(cacheDir string) string {
	if cacheDir == "" {
		return ""
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
// Through the parser rather than a pattern, for the reason ParseCommands exists:
// it resolves quoting, peels wrappers, and hands back each command a line would
// actually run, so `mise exec -- ./magus affected ci` reads the same as the bare form
// and a `ci` inside a quoted argument reads as neither.
func commandRunsGate(command string) bool {
	cmds, ok := ParseCommands(command)
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

// gateCadence names the cost, the command that sizes the risk, and leaves the trade to
// the caller.
//
// Not a threshold, and the earlier drafts that were one are the reason. A count alone
// changes nothing: this fired eight times in one session at a caller who gated ten times
// in two hours and narrowed nothing. "Once per branch, when the change is complete" fails
// the other way, since complete is a judgment nobody can check. And a hard rule -- more
// than one project, or the root -- is a refusal dressed as advice: this tier cannot
// enforce anything, and the caller knows things magus does not, like whether the change
// is load-bearing or what a red pull request costs today.
//
// What magus can contribute is the size of the blast radius, which is what --plan prints.
// The caller weighs that against the wall clock above.
var gateCadence = "`" + hint.Affected.With(string(types.TargetCI), "--plan") +
	"` sizes the risk: one project is usually answered by that project's own `" + string(types.TargetCI) +
	"`, while the root or several projects is the gate earning it."

func gateRepeatAdvice(runs int, spent time.Duration) string {
	return fmt.Sprintf("magus workspace: the `%s` gate has run %d times here in the last %s, about %s of wall clock. %s\n",
		types.TargetCI, runs, gateRepeatWindow, spent.Round(time.Second), gateCadence)
}

// gateRepeatBrief is the repeat form, and it keeps the running cost rather than going
// quiet: the count and the wall clock are the whole argument, and they are the part that
// has changed since the caller last read the full text.
func gateRepeatBrief(runs int, spent time.Duration) string {
	return fmt.Sprintf("magus workspace: `%s` has run %d times here, about %s of wall clock. %s\n",
		types.TargetCI, runs, spent.Round(time.Second), gateCadence)
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
// with no live row and an unreadable ledger all pass: a boundary nobody declared is not a
// narrow one. A person running their own gate names no lease and never reaches this rule.
//
// A bound row that declares NO check is refused too. It used to pass, on the reading that
// an empty field is an undeclared boundary, but a bound worker is a worker either way and
// the gate is still the orchestrator's; the empty field says nobody wrote down what this
// unit should run, which is a reason to ask rather than a licence to run everything.
func denyLeaseScopedGate(ctx context.Context, deps Dependencies, actingLease, command string) string {
	if actingLease == "" || !commandRunsGate(command) {
		return ""
	}
	me, ok := actingLiveLease(ctx, deps, actingLease)
	if !ok || LeaseOwnsGate(me) {
		return ""
	}
	if me.Validation == "" && me.Check == nil && len(me.CompletionGates) == 0 {
		return fmt.Sprintf(
			"magus workspace: lease %s declares no check, so the `%s` gate is not yours to run. The orchestrator gates once, in its own tree, after every unit lands.\n"+
				"Run the narrowest target covering your paths and report what it said. "+leaseActorClause("record a check on this row"),
			me.ID, types.TargetCI)
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
func actingLiveLease(ctx context.Context, deps Dependencies, actingLease string) (types.Job, bool) {
	standing := actingLeaseStanding(ctx, deps, actingLease)
	if !standing.declared || !standing.state.Live() {
		return types.Job{}, false
	}
	return standing.row, true
}

// leaseStanding is where the acting lease's row stands in the ledger: whether the ledger
// could be read at all, whether it declares the id, the state it recorded, and the row.
//
// Three answers, not two. "The ledger says nothing" and "the ledger could not be read" are
// the difference between an id nobody declared and a rule the guard cannot evaluate, and
// only the first is the caller's mistake.
type leaseStanding struct {
	readable bool
	declared bool
	state    types.JobState
	row      types.Job
	// rows is the whole store, for a rule that reads the job tree around row.
	rows []types.Job
}

// terminal reports a row that is declared and has stopped running, so its rules are inert.
func (s leaseStanding) terminal() bool {
	return s.declared && !s.state.Live()
}

// inFlight reports a row whose work is still to come: declared or running. A holder may
// take another job only once its own is not, since walking away mid-flight would leave
// its next write graded under a job nobody handed it. Exited is not in flight although it
// is live: nobody is required to ever wait on it, and a caller held to it would be stuck.
func (s leaseStanding) inFlight() bool {
	return s.declared && (s.state == types.StateDeclared || s.state == types.StateRunning)
}

// actingLeaseStanding looks the acting lease up across EVERY row, terminal ones included.
//
// Terminal ones included, because "does this id mean anything here" is what separates a
// typo from a plan that has already finished, and the two cases want opposite verdicts: a
// dead id looked exactly like a guarded session.
func actingLeaseStanding(ctx context.Context, deps Dependencies, actingLease string) leaseStanding {
	if !types.ValidJobID(actingLease) {
		return leaseStanding{}
	}
	location := hookLocation(ctx, deps)
	if location.cacheDir == "" {
		return leaseStanding{}
	}
	leases, err := leaseRows(ctx, location)
	if err != nil {
		return leaseStanding{}
	}
	for _, u := range leases {
		if u.ID == actingLease {
			return leaseStanding{readable: true, declared: true, state: u.State, row: u, rows: leases}
		}
	}
	return leaseStanding{readable: true, rows: leases}
}

// denyUndeclaredLease refuses to grade a call for an id this workspace's ledger does not
// carry, and returns "" whenever it does carry one or cannot say.
//
// An id that names no row buys SILENT un-enrolled treatment: every lease-scoped rule reads
// it, finds nothing, and passes, so a worker whose orchestrator typo'd the id runs
// unguarded and looks exactly like a guarded one. It denies rather than advises because the
// caller asserted a binding that does not exist.
//
// Not the INVALID-id case, which stays an advisory (adviseInvalidLease): an id magus cannot
// parse is one it cannot look up either, and blocking a tool call over unparsable metadata
// is the failure the fail-open contract is written against.
//
// It refuses WORK and never the remedy. command is the shell line on the command surface
// and "" on the write surface, where every call is work by definition. A rule that refused
// the line it prints locked a checkout out of its own repair: the plan moved house once and
// the bound worker could not read the plan, print a schema, or bind again, because each of
// those is a command and every command was refused.
func denyUndeclaredLease(standing leaseStanding, actingLease, command string) string {
	if !standing.readable || standing.declared {
		return ""
	}
	if command != "" && undeclaredLeaseRepairs(command) {
		return ""
	}
	return fmt.Sprintf("magus workspace: lease %s is not declared; run `%s` to see the plan.\n"+
		"Every lease-scoped rule reads that row, so a call naming a row this workspace's ledger does not carry is graded by nothing at all. That is the shape a typo'd id takes: an agent that believes it is inside a boundary, running outside every one.\n"+
		"Reading the tree, printing a schema or a usage line, and the job verbs themselves still run, so you can find the id you were handed and bind to it.",
		actingLease, hint.LsJobs.String())
}

// undeclaredLeaseRepairs reports a shell line that only looks at this checkout or repairs
// the binding, so an unenrolled session can find out what it is bound to and fix it.
//
// EVERY invocation on the line has to qualify. One reader chained onto a build is a build,
// and the cheap way to get work past this rule would be to put `ls &&` in front of it.
func undeclaredLeaseRepairs(command string) bool {
	cmds, ok := ParseCommands(command)
	if !ok || len(cmds) == 0 {
		return false
	}
	for _, c := range cmds {
		if !repairInvocation(c) {
			return false
		}
	}
	return true
}

// repairInvocation judges one invocation. magus answers for itself: a usage line or a
// schema touches no store, and the verbs that read the plan or take a job are the remedy.
// Everything else is the short reader set a person orients with, which is deliberately
// narrower than cacheDirReaders (git and magus are wholesale readers there, and `git
// commit` is not a way to find out what you are bound to).
func repairInvocation(c hint.Invocation) bool {
	if path.Base(c.Name) == "magus" {
		if slices.ContainsFunc(c.Args, func(a string) bool { return a == "--schema" || a == "--help" || a == "-h" }) {
			return true
		}
		words := magusSubcommandWords(c.Args)
		return len(words) > 0 && undeclaredLeaseVerbs[words[0]] && !renamesSymbol(c.Args)
	}
	if c.Name == "git" {
		g := parseGit(c.Args)
		return g.alias == "" && gitReadVerbs[g.sub]
	}
	return undeclaredLeaseReaders[path.Base(c.Name)]
}

// The three sets repairInvocation reads. The magus verbs are the ones that answer "what am
// I bound to and what does this workspace hold"; the job verbs are in it because taking a
// job is how a checkout stops being unenrolled.
var (
	undeclaredLeaseVerbs = map[string]bool{
		"ledger": true, "job": true, "session": true, "ls": true, "describe": true,
		"query": true, "explain": true, "path": true, "refs": true, "where": true,
		"status": true, "doctor": true, "help": true, "version": true,
	}
	gitReadVerbs = map[string]bool{
		"status": true, "log": true, "diff": true, "show": true, "branch": true, "remote": true,
	}
	undeclaredLeaseReaders = map[string]bool{
		"cat": true, "head": true, "tail": true, "ls": true, "grep": true, "rg": true,
		"find": true, "stat": true, "wc": true, "pwd": true, "echo": true,
	}
)

// adviseInvalidLease says that an id magus cannot parse was treated as naming no lease.
//
// Treated as absent rather than rejected: an id magus cannot parse is one it cannot look
// up either, and erroring would block the tool call over metadata. Said on EVERY surface,
// from Judge: it used to be produced inside gradeLeasedWrite, so a command-surface call
// under a typo'd id ran fully un-enrolled with no notice at all.
func adviseInvalidLease(actingLease string) string {
	if actingLease == "" || types.ValidJobID(actingLease) {
		return ""
	}
	return fmt.Sprintf(
		"magus workspace: fix the lease id and re-run, so the guard can grade this call against your lease's declared boundary.\n"+
			"%s=%q is not a valid lease id (at most %d characters of A-Za-z0-9-_./:), so this call was graded as if it named no lease.",
		envHookLease, actingLease, types.MaxJobIDLen)
}

// adviseTerminalLease says that a declared row has stopped running, or "" for a live one.
//
// The rules keyed on the row are inert from that moment (liveLeases drops it), which is
// correct and invisible: the verdicts look identical to a session nobody leased. One line
// per session is what makes the difference readable.
func adviseTerminalLease(standing leaseStanding, actingLease string) string {
	if !standing.terminal() {
		return ""
	}
	return fmt.Sprintf("magus workspace: lease %s is in state %s; its rules are inert.\n"+
		"A terminal row has no boundary left to grade against, so the lease-scoped denials are not running for this session. If work is still in flight under that id, the orchestrator owns reopening it.",
		actingLease, standing.state)
}

// denyOverdueLease refuses a write graded under a live lease past its deadline, or returns
// "" for one with no deadline or time left. The row stays live: ending it is the
// orchestrator's, and magus never transitions a row on its own.
func denyOverdueLease(me types.Job, now int64) string {
	if !me.Overdue(now) {
		return ""
	}
	return fmt.Sprintf("magus workspace: stop writing and report what you have; lease %s passed its deadline.\n"+
		"Lease %s was forked with a timeout, and its deadline %s passed %s ago, so the guard denies every write graded under it. "+
		"The row is still %s: the orchestrator ends it with `%s` or re-forks it with a new --timeout.",
		me.ID, me.ID, time.Unix(me.Deadline, 0).UTC().Format(time.RFC3339),
		time.Duration(now-me.Deadline)*time.Second, me.State, hint.JobExit.With(me.ID))
}

// denyLeaseScopedRebind refuses, under a bound lease, every command that would rewrite
// WHO the caller is or what its row says.
//
// The store refuses these too, on the ACTING LEASE id; registered_by is provenance
// recorded beside it and is read by no rule. This rule is the half that arrives BEFORE the
// call, carrying the reason: an agent that reads a denial naming the tool reaches for the
// tool. Being told first costs one verdict; finding out from a store error costs a turn
// and teaches nothing about why.
//
// A READ is untouched. `magus session lease` with no operand prints the binding, and
// `magus ledger ls` prints the plan; refusing those would deny a worker the ability to
// find out what it is bound to, which is the opposite of what this rule is for.
//
// Unbound callers are untouched too, for the reason every lease rule here gives: an
// orchestrator and a person at a terminal both name no lease, and they are the ones who
// write rows.
//
// A child of the caller's own lease, and `job wait` on one of its descendants, pass: the
// store grades a child against its parent (see childForkRebind for when it cannot).
func denyLeaseScopedRebind(ctx context.Context, deps Dependencies, actingLease, command string) string {
	if actingLease == "" {
		return ""
	}
	f, err := parseFile(command, DialectBash)
	if err != nil {
		return ""
	}
	h := holder{
		id:       actingLease,
		standing: sync.OnceValue(func() leaseStanding { return actingLeaseStanding(ctx, deps, actingLease) }),
		storeLease: func() string {
			lease, _ := job.ActingLease(hookLocation(ctx, deps).cacheDir, "")
			return lease
		},
	}
	var what string
	syntax.Walk(f, func(n syntax.Node) bool {
		st, ok := n.(*syntax.Stmt)
		if !ok || what != "" {
			return what == ""
		}
		for _, c := range stmtCommands(st, DialectBash) {
			if what = leaseRebind(c, stdinRecord(st), h); what != "" {
				return false
			}
		}
		return true
	})
	if what != "" {
		return fmt.Sprintf(
			"magus workspace: leave your own job alone. "+leaseActorClause("change a job")+"\n"+
				"`%s` would %s, and this call acts under lease %s. An agent that can move the rows it is graded against is graded against a boundary nobody handed it from the next call on, which is the one thing the ledger exists to make visible.",
			command, what, actingLease)
	}
	return ""
}

// denyLeaseScopedHarness refuses, under a lease, the commands that rewire a host harness.
// The hook wiring is what lets the guard see an agent at all, so a worker that could
// rewrite it could stop being graded.
//
// `magus agent harness` refuses the same commands itself, but a CLI process knows only the
// checkout's binding and its own BAGGAGE claim. The guard also knows the calling subagent
// and the host session, so a worker attributed by either is refused here.
func denyLeaseScopedHarness(_ context.Context, _ Dependencies, actingLease, command string) string {
	if actingLease == "" {
		return ""
	}
	cmds, ok := ParseCommands(command)
	if !ok {
		return ""
	}
	for _, c := range cmds {
		if path.Base(c.Name) != "magus" || magusFlag(c.Args, "h") || magusFlag(c.Args, "help") {
			continue
		}
		words := magusSubcommandWords(c.Args)
		for _, rewire := range []hint.Command{hint.AgentHarnessApply, hint.AgentHarnessInstall, hint.AgentHarnessRemove} {
			if rewire.MatchedBy(words) {
				return fmt.Sprintf(
					"magus workspace: leave the host harness alone. "+leaseActorClause("rewire a host harness")+"\n"+
						"`%s` would rewrite the hook wiring the guard grades your calls through, and this call acts under lease %s.",
					command, actingLease)
			}
		}
	}
	return ""
}

// holder is what the rebind rule reads about the bound caller, each part fetched only on
// the paths that need it.
type holder struct {
	id       string
	standing func() leaseStanding
	// storeLease is the lease the job store writes as from this checkout, "" for none.
	storeLease func() string
}

// leaseRebind names what a parsed command would do to the ledger when it is one a bound
// caller may not do, or "" for everything else. stdin is the record the command's
// statement feeds it, "" when the line carries none the guard can read.
//
// The MCP form is graded here alongside the CLI ones because it is the SAME write through
// a different transport, and a rule that held on one channel would move the traffic rather
// than stop it.
func leaseRebind(c hint.Invocation, stdin string, h holder) string {
	if c.Name == hint.ToolJob.String() {
		return jobToolRebind(mcpParams(c.Args), h)
	}
	if path.Base(c.Name) != "magus" {
		return ""
	}
	// A usage line and a schema are reads: neither reaches a store, and the rule below
	// reads the subcommand without them, so `job wait --schema` was refused to the one
	// caller who needs the shape of the result it has to file.
	if slices.ContainsFunc(c.Args, func(a string) bool { return a == "--schema" || a == "--help" || a == "-h" }) {
		return ""
	}
	words := magusSubcommandWords(c.Args)
	if len(words) < 2 {
		return ""
	}
	// A holder must always be able to READ the rubric it is graded against. These print the
	// contract and touch no row, and the schema is the same for every row, so refusing them
	// protects nothing and guarantees results written from memory of another vocabulary by
	// the people most expected to file good ones.
	if hasFlag(c.Args, 0, "schema") || hasFlag(c.Args, 0, "help") {
		return ""
	}
	switch {
	// Taking the job the caller already holds is the bootstrap run twice, and records the
	// base it landed on. Taking another is the escape this rule closes, but only while the
	// held job is in flight: once it has exited or ended, or names no row at all, the next
	// exec is how the caller moves on (see bindOnExec).
	case hint.JobExec.MatchedBy(words) && len(words) > 2:
		if held := h.standing(); !held.inFlight() || execOperand(c.Args) == held.row.ID {
			return ""
		}
		return "take the lease on another job"
	case words[0] == hint.JobWait.Head() && words[1] == hint.JobWait.Leaf():
		if len(words) > 2 && descendsFrom(h.standing(), words[2]) {
			return ""
		}
		return "verify a job"
	case words[0] == hint.JobFork.Head() && words[1] == hint.JobFork.Leaf():
		return childForkRebind(cliFork(c.Args, stdin), h, "declare a job")
	}
	return ""
}

// descendsFrom reports whether id sits below the held row in the parent chain.
func descendsFrom(held leaseStanding, id string) bool {
	return held.declared && held.state.Live() &&
		slices.ContainsFunc(types.JobDescendants(held.rows, held.row.ID), func(r types.Job) bool { return r.ID == id })
}

// childFork is the row a fork declares, as far as childForkRebind reads it.
type childFork struct {
	id, parent string
	readOnly   bool
	writePaths bool
	// bounded is any declaration beyond the lineage, the prose and read_only: paths, a
	// check, gates, a state. Each is something the store grades against the parent.
	bounded bool
}

// childForkRebind names what a bound caller's fork would do when the guard refuses it, or
// "" for a new child of its own lease that is left to the store. verb opens the refusal.
//
// The store grades the child's boundary against the parent's (internal/job/authorize.go
// authorizeChild), but only when it writes AS that lease, which it learns from this
// checkout's record. A worker the hook identified in a checkout bound to nobody writes to
// an unbound store that grades nothing, so there only a read-only child declaring no
// boundary passes: it can widen nothing.
//
// A child that is neither read-only nor handed write paths is refused on both paths: an
// empty write set scopes nothing (gradeAgainstOwnLease), and the store's subset test
// passes it.
func childForkRebind(f childFork, h holder, verb string) string {
	held := h.standing()
	switch {
	case !held.declared || !held.state.Live():
		return verb
	case f.id == "":
		return fmt.Sprintf("%s the guard cannot read: give the child an id and --parent %s, or its record in a quoted heredoc", verb, h.id)
	case f.id == h.id:
		return "rewrite the job it holds"
	case f.parent != h.id:
		return fmt.Sprintf("%s outside its own tree: a row a worker creates must name %s as its parent, and this one names %q", verb, h.id, f.parent)
	case slices.ContainsFunc(held.rows, func(r types.Job) bool { return r.ID == f.id }):
		return fmt.Sprintf("%s over %s, which already exists: a worker writes no row but its own and the children it hands out", verb, f.id)
	case !f.readOnly && !f.writePaths && (held.row.ReadOnly || len(held.row.WritePaths) > 0):
		return verb + " that can write anywhere: a child that is not read-only and names no write paths is scoped by nothing, so fork it --read-only or with --write-paths inside your own"
	case (!f.readOnly || f.bounded) && h.storeLease() != h.id:
		return fmt.Sprintf("%s nothing would grade: this checkout's job store writes as %q, not as %s, so it checks no child against your row; only a --read-only child declaring no paths, check or gates passes here",
			verb, h.storeLease(), h.id)
	}
	return ""
}

// forkFlagsUnbounded are the `job fork` flags, and the magus_job params, that declare no
// boundary: the lineage, the prose, the timing and read_only.
var forkFlagsUnbounded = map[string]bool{
	"parent": true, "criteria": true, "model": true, "timeout": true, "checkpoint": true,
	"read-only": true, "read_only": true, "depends-on": true, "depends_on": true,
	"stdin": true, "op": true, "id": true,
}

// cliFork reads the child a `magus job fork` argv declares. Under --stdin the flags are
// ignored and the row is the record, which the guard reads only from a heredoc or a
// here-string on the same statement; a record it cannot read declares no id.
func cliFork(args []string, stdin string) childFork {
	operands, flags := verbArgv(args, hint.JobFork)
	if flags["stdin"] != "" && flags["stdin"] != "false" {
		row, err := job.DecodeDeclaration(strings.NewReader(stdin))
		if err != nil {
			return childFork{}
		}
		return childFork{
			id: row.ID, parent: row.Parent, readOnly: row.ReadOnly, writePaths: len(row.WritePaths) > 0,
			bounded: len(row.WritePaths)+len(row.ReadPaths)+len(row.DenyPaths)+len(row.CompletionGates) > 0 ||
				row.Check != nil || row.Validation != "" || (row.State != "" && row.State != types.StateDeclared),
		}
	}
	f := childFork{parent: flags["parent"], writePaths: flags["write-paths"] != ""}
	if len(operands) == 1 {
		f.id = operands[0]
	}
	if v, ok := flags["read-only"]; ok {
		f.readOnly = v != "false"
	}
	for name := range flags {
		if _, own := verbFlag(hint.JobFork, name); own && !forkFlagsUnbounded[name] {
			f.bounded = true
		}
	}
	return f
}

// verbArgv splits a magus argv into the operands after its two-word verb and the flags it
// carries, a bool flag reading "true". Arity comes from the registry the CLI parses with;
// a flag it does not know takes no value, and the CLI refuses that argv anyway.
func verbArgv(args []string, verb hint.Command) (operands []string, flags map[string]string) {
	flags = map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			operands = append(operands, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			operands = append(operands, a)
			continue
		}
		name, value, joined := strings.Cut(strings.TrimLeft(a, "-"), "=")
		takes, _ := MagusFlagTakesValue(name)
		if own, ok := verbFlag(verb, name); ok {
			takes = own.Kind != cli.FlagBool
		}
		switch {
		case joined:
		case takes && i+1 < len(args):
			i++
			value = args[i]
		default:
			value = "true"
		}
		flags[name] = value
	}
	return operands[min(2, len(operands)):], flags
}

// verbFlag looks name up among verb's own flags in the registry the CLI parses with.
func verbFlag(verb hint.Command, name string) (cli.Flag, bool) {
	for _, group := range cli.All {
		if group.Name != verb.Head() {
			continue
		}
		for _, sub := range group.Children {
			if sub.Name != verb.Leaf() {
				continue
			}
			if i := slices.IndexFunc(sub.Flags, func(f cli.Flag) bool { return f.Name == name }); i >= 0 {
				return sub.Flags[i], true
			}
		}
	}
	return cli.Flag{}, false
}

// stdinRecord is the text a statement feeds its command from a heredoc or a here-string,
// or "" when it feeds none or the text holds an expansion only the shell can resolve.
func stdinRecord(st *syntax.Stmt) string {
	for _, r := range st.Redirs {
		w := r.Hdoc
		if r.Op == syntax.WordHdoc {
			w = r.Word
		}
		if w == nil {
			continue
		}
		if !literalParts(w.Parts) {
			return ""
		}
		return literalWord(w.Parts)
	}
	return ""
}

// literalParts reports whether a word holds only text the shell passes through unchanged.
func literalParts(parts []syntax.WordPart) bool {
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit, *syntax.SglQuoted:
		case *syntax.DblQuoted:
			if !literalParts(p.Parts) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// jobToolRebind judges one call to the job tool against the job the caller holds, naming
// what the call would do when it is something a holder may not.
//
// Four carve-outs, and each is somebody else's job rather than a hole:
//
//   - `op=exec` on the caller's OWN job records the base it landed on. That is the
//     holder's own procedure, demanded by the checkpoint denial in gradeAgainstOwnLease,
//     and denying it here would leave a holder unable to write anywhere at all. On
//     another job it is refused only while the held one is in flight, as the CLI form is.
//   - `op=fork` on the caller's own job that only SHRINKS its write paths gives ground
//     back. It passes to the store, which owns whether a given shrink is legitimate; the
//     guard's job is the direction, and giving up a path cannot widen a role.
//   - `op=fork` of a new row naming the caller's job as `parent` is a child, graded by
//     the store against its parent (childForkRebind).
//   - a read (`op=list`, and the default) is not a write.
//
// Shrinking is verbatim membership, not glob containment: every declaration in the call
// must already be one the job carries, and there must be fewer of them. A cleverer pattern
// that happens to cover less is not something this rule will try to prove.
func jobToolRebind(params map[string]string, h holder) string {
	op, id := params["op"], params["id"]
	if op == "clear" {
		return "drop every job"
	}
	if op != "fork" && op != "exec" {
		return ""
	}
	standing := h.standing()
	if !standing.readable || standing.terminal() {
		// The store could not answer, or the job has already finished and every rule
		// keyed on it is inert. Naming somebody else's job here would be a reason the
		// caller can check and find false, in the same verdict that tells them these
		// rules are not running.
		return ""
	}
	if op == "exec" && (id == standing.row.ID || !standing.inFlight()) {
		return ""
	}
	if _, entering := params["enter"]; entering && op == "fork" {
		// The store refuses anything but an entry beside `enter`, and grades the rest.
		if id != standing.row.ID && mayHandOut(standing.rows, standing.row.ID, id) {
			return ""
		}
		return "enter a job not forked beneath the one it holds"
	}
	if op == "fork" && id != standing.row.ID {
		return childForkRebind(mcpFork(params), h, "write another job")
	}
	if id == "" || id != standing.row.ID {
		return "write another job"
	}
	row := standing.row
	if shrinksWritePaths(params, row) {
		return ""
	}
	return "rewrite the job it holds"
}

// mcpFork reads the child an `op=fork` call declares.
func mcpFork(params map[string]string) childFork {
	f := childFork{
		id: strings.TrimSpace(params["id"]), parent: strings.TrimSpace(params["parent"]),
		readOnly:   params["read_only"] == "true",
		writePaths: params[writePathsParam]+params[writePathsLegacyParam] != "",
	}
	for key := range params {
		f.bounded = f.bounded || !forkFlagsUnbounded[key]
	}
	return f
}

// shrinksWritePaths reports whether a put changes nothing but the row's write paths, and
// only by removing entries it already carries.
//
// An ALLOWLIST over what the call carried, not a scan of the keys the guard happens to
// know: a key outside the three below is a rewrite of something else on the row whatever
// it holds, and reading a list of known keys instead means every key added to the ledger's
// merge is cleared here until somebody remembers to add it in two places.
func shrinksWritePaths(params map[string]string, row types.Job) bool {
	declared, present := "", false
	for key, value := range params {
		switch key {
		case "op", "id":
		case writePathsParam, writePathsLegacyParam:
			if present {
				// Both spellings at once: nothing says which the store would apply, so
				// this is not a shrink anyone can prove.
				return false
			}
			declared, present = value, true
		default:
			return false
		}
	}
	proposed := strings.Split(declared, ",")
	if !present || len(proposed) >= len(row.WritePaths) {
		return false
	}
	for _, decl := range proposed {
		if !slices.Contains(row.WritePaths, strings.TrimSpace(decl)) {
			return false
		}
	}
	return true
}

// LeaseOwnsGate reports whether a lease's declared check IS the gate, in which case the
// lease owns it and nothing is refused.
//
// The check RECORD decides it whenever the row carries one: a target field cannot be
// confused by a stray word the way a rendered line can.
func LeaseOwnsGate(row types.Job) bool {
	_, owns := LeaseGateSource(row)
	return owns
}

// LeaseGateSource is LeaseOwnsGate plus WHICH declaration named the gate, so a refusal can
// quote the offending one.
//
// It exists because the refusal quoted row.Validation whatever had actually matched, and a
// row can now name the gate from any of several places: told that its check `go-test api`
// names `ci`, a reader goes looking for a bug in a line that is fine while the completion
// gate that really did it goes unmentioned.
func LeaseGateSource(row types.Job) (string, bool) {
	// EVERY gate is asked, and the primary check no longer answers for the row. Returning
	// on row.Check alone meant a job whose check was some narrow target stopped the walk
	// before its completion gates were read, so `--check go-test api --gate-check ci` --
	// the shape the two flags invite -- took the gate through the door the check rule
	// holds shut.
	for _, gate := range row.EffectiveCompletionGates() {
		if gate.Kind != types.GateKindCheck {
			continue
		}
		t, err := types.ParseTarget(gate.Check.Target)
		if err == nil && t.Name == types.TargetCI {
			if gate.ID == types.PrimaryCompletionGateID {
				return gate.Check.String(), true
			}
			return fmt.Sprintf("completion gate %q (%s)", gate.ID, gate.Check.String()), true
		}
	}
	if validationNamesGate(row.Validation) {
		return row.Validation, true
	}
	return "", false
}

// compat(until: no ledger row is written without a check record): rows predating
// types.LeaseCheck carry only the rendered validation line, so it is read as words. `ci`,
// `magus run ci` and `affected ci` are all things a person writes there, and a stray `ci`
// elsewhere in the field reads as ownership and clears the deny, which is the direction
// this rule fails in on purpose. Observe it is safe to drop when every row
// `magus ledger list -o json` prints carries a `check` object.
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
func denyLeaseScopedVCS(ctx context.Context, deps Dependencies, actingLease, command string) string {
	if actingLease == "" || helpOnlyLine(command, DialectBash) {
		return ""
	}
	cmds, ok := ParseCommands(command)
	if !ok {
		return ""
	}
	for _, c := range cmds {
		op := vcsMutation(c)
		if op == "" {
			continue
		}
		me, ok := actingLiveLease(ctx, deps, actingLease)
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

// isPush reports whether a parsed command publishes: git push, hg push, sl push (with or
// without --to) or jj git push, relocated or not.
func isPush(c hint.Invocation) bool {
	return strings.HasSuffix(vcsMutation(c), " push")
}

// vcsSubcommand splits a VCS invocation at its subcommand, past the global options before it.
func vcsSubcommand(c hint.Invocation) (sub string, rest []string) {
	if c.Name == "git" {
		g := parseGit(c.Args)
		return g.sub, g.rest
	}
	v := parseVCS(c.Name, c.Args)
	return v.sub, v.rest
}

// vcsMutation names the version-control operation a parsed command performs when it is one
// a worker must leave to the orchestrator, or "" for anything else. Global options before
// the subcommand (git -C <dir>, hg -R <repo>, jj -R <repo>) are skipped so a relocated
// commit is still a commit.
//
// Every backend's PUSH is read, because the push gate keys on it and a push it cannot see
// publishes without the person being asked. The other mutations are git's alone: the
// Mercurial, Sapling and Jujutsu arms of the guard grade their destructive verbs in
// nonGitVCSGuard, and a name here that promised more would be a rule nothing enforces.
//
// `git stash list` and `git stash show` read the stash rather than moving work onto
// it, so they pass; every other stash form is a mutation.
func vcsMutation(c hint.Invocation) string {
	sub, rest := vcsSubcommand(c)
	// The word an inline alias defines can run any verb, push included.
	if _, other := vcsGlobals[c.Name]; other {
		if v := parseVCS(c.Name, c.Args); v.alias != "" {
			return c.Name + " " + aliasFlag(v.alias)
		}
	}
	switch c.Name {
	case "hg", "sl":
		if sub == "push" {
			return c.Name + " push"
		}
		return ""
	case "jj":
		// `git` is a command group, so its own subcommand is read past jj's global options
		// the same way.
		if nested, _ := vcsSubcommand(hint.Invocation{Name: c.Name, Args: rest}); sub == "git" && nested == "push" {
			return "jj git push"
		}
		return ""
	case "git":
		// The word an inline alias defines can run any of the verbs below.
		if g := parseGit(c.Args); g.alias != "" {
			return "git -c " + g.alias
		}
	default:
		return ""
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

// denyWriteOutsideLease refuses a shell line that writes outside the acting lease's
// write paths, in the words the path surface would have used for the same file.
//
// A shell line is graded because a host reports no PATH for one: without this, a bound
// worker that redirects, tees, sed -i's or cp's into a sibling's tree passes while the
// identical editor-tool write is denied.
//
// It calls the path surface's own grader rather than deciding anything itself, so the two
// cannot drift about who owns a path or how the refusal reads, and the extraction that
// finds the written words is the cache-dir rule's, shared for the same reason.
//
// Only a candidate some live lease DECLARED is graded. The extraction offers every word a
// non-reader command was pointed at, which is the safe direction for a boundary as
// specific as the cache dir but not for one as ordinary as a path: grading every word
// would refuse `echo hi` for writing outside the boundary.
func denyWriteOutsideLease(ctx context.Context, deps Dependencies, actingLease, command string) string {
	if actingLease == "" {
		return ""
	}
	location := hookLocation(ctx, deps)
	if location.workspace == "" {
		return ""
	}
	rows, err := leaseRows(ctx, location)
	if err != nil {
		return ""
	}
	live := liveLeases(rows)
	if len(live) == 0 {
		return ""
	}
	dialect := effectiveDialect(deps.ShellDialect)
	redirects := redirectTargets(command, 0, dialect)
	for _, candidate := range writeTargetCandidates(command, 0, dialect) {
		// A flag (`-p`, `-c`) or stdin's `-` names no file; a file spelled that way is
		// written `./-p`, which still resolves.
		if strings.HasPrefix(candidate, "-") {
			continue
		}
		rel, inside := workspaceRelative(location.workspace, candidate)
		if !inside {
			continue
		}
		declared, byCatchAll := declaredPath(live, rel)
		if !declared {
			continue
		}
		// A catch-all covers every word, `print(1)` included, so under one only a
		// redirect target or a word shaped like a file stands in for a path.
		if byCatchAll && !slices.Contains(redirects, candidate) && !pathShaped(location.workspace, rel) {
			continue
		}
		if g := gradeLeasedWrite(ctx, deps, actingLease, candidate); g.Decision == "deny" {
			return g.Reason
		}
	}
	return ""
}

// declaredPath reports whether any live lease named rel in a boundary, as a path it owns
// or a path it was refused, and whether only a catch-all (`**`) did. A word no plan
// mentions is not treated as a path at all.
func declaredPath(live []types.Job, rel string) (declared, byCatchAll bool) {
	for _, u := range live {
		for _, decls := range [][]string{u.WritePaths, u.DenyPaths} {
			decl, ok, _ := declarationCovering(decls, rel)
			if !ok {
				continue
			}
			if !catchAll(decl) {
				return true, false
			}
			declared = true
		}
	}
	return declared, declared
}

// catchAll reports a declaration that covers every path in the workspace.
func catchAll(decl string) bool {
	file, _ := types.SplitClaim(decl)
	d := path.Clean(file)
	for {
		trimmed := strings.TrimSuffix(strings.TrimSuffix(d, "/**"), "/*")
		if trimmed == d {
			break
		}
		d = trimmed
	}
	return d == "**" || d == "*"
}

// pathShaped reports a word that reads as a file: one that exists, or one spelled in
// file-name characters with a separator or an extension. `hello`, `600` and `print(1)`
// are not; `notes.txt` and `out/log` are. A new bare name (`touch build`) is missed,
// failing open like every other uncertainty here.
func pathShaped(workspace, rel string) bool {
	if _, err := os.Lstat(filepath.Join(workspace, filepath.FromSlash(rel))); err == nil {
		return true
	}
	if !strings.ContainsAny(rel, "./") || strings.Trim(rel, "0123456789.") == "" {
		return false
	}
	return !strings.ContainsFunc(rel, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("._-/~@+,%:", r)
	})
}
