package guard

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
)

// The waiter rules: a call or a spawn whose whole job is to wait. Work an agent starts is
// announced when it ends, so a waiter adds nothing but wake-ups, and a pile of them keeps
// waking an agent long after the work it watched is done. The watcher rules are in
// internal/guard/monitor.go.

const (
	// A status read after a sleep, or a backgrounded call that only sleeps.
	denyRuleSleepPoll denyRuleName = "sleep-poll"
	// A spawn whose brief is to wait on, poll or watch other work.
	denyRuleSpawnWaiter denyRuleName = "spawn-waiter"
	// A spawn for a job that already has a worker.
	denyRuleSpawnDuplicate denyRuleName = "spawn-duplicate"
)

// advisorySpawnNestedWaiter is a subagent spawning a helper to wait or watch.
const advisorySpawnNestedWaiter hint.MarkerKind = "spawn-nested-waiter"

// sleepPollFloor is the shortest sleep read as a wait rather than a settle: a second or two
// lets a file land or a port open, and nobody polls a remote status every second.
const sleepPollFloor = 10 * time.Second

var (
	denySleepPoll = fmt.Sprintf("a sleep before a status read is a poll spread over calls; background the work as `%s` and wait for its notice.",
		boundedRunWait)
	denySleepPollWhy = "Each call spends a turn and the sleep's tool slot to look once, and the next call looks again. " +
		"A backgrounded run is announced when it ends, so the notice answers when the state changes, where a sleep answers on its own schedule."

	denySleepTimer    = "a call that only sleeps is a timer; a backgrounded command already announces itself when it ends, so wait for that notice or do other work meanwhile."
	denySleepTimerWhy = "The work's own backgrounded call is the waiter: its notice arrives with the result, where a timer's says only that time passed. " +
		"A timer wakes you on a guess, every timer still pending wakes you again after the work is done, and a foreground sleep past the tool timeout is moved to the background and becomes one."

	denySlotWait = fmt.Sprintf("a magus run waits for the build budget itself, up to %s, and names who holds it: run `%s <your check>` and let it wait. A watch, a sleep before a run, or a wait on another run's pid waits for nothing.",
		cache.BudgetWaitSource, hint.Run.String())
	denySlotWaitWhy = "A run's claim queues at the broker and is seated the moment room frees, in the call that spends the seat. " +
		"A wait in front of it holds nothing, so the run still queues behind whoever asked first, and a worker looping on reads spends turns no one who can free a seat sees."

	budgetFullWhy = "Every worker's check needs a seat in the build budget, and a worker's check runs with --budget-wait 0, so a worker that finds none is refused at once and files the check owed; spawning into a full budget spends a worker's turns on refusals. " +
		"The budget is the machine's, shared by every worktree, so it frees only as the runs holding it end."
)

// slotWaitLine reports a line that waits for build budget outside the run
// that spends it. A status watch, a status read by a sleep or watcher, a
// run retried after sleepPollFloor and budgetHolderWait all do.
func slotWaitLine(ctx context.Context, command string, d Dialect, watcher bool) bool {
	f, err := parseFile(command, d)
	if err != nil {
		return false
	}
	var slept time.Duration
	napped := false
	eachCall(f, d, func(_ *syntax.CallExpr, c hint.Invocation) {
		if dur, ok := sleepDuration(c); ok {
			slept, napped = max(slept, dur), true
		}
	})
	waits := false
	if budgetHolderWait(ctx, command, d) {
		return true
	}
	eachCall(f, d, func(_ *syntax.CallExpr, c hint.Invocation) {
		if !isMagusInvocation(c) {
			return
		}
		words := magusSubcommandWords(c.Args)
		if len(words) == 0 {
			return
		}
		switch {
		case isServerStatus(words):
			waits = waits || watcher || napped || statusFlag(c.Args, "watch", "W")
		case words[0] == "run", words[0] == "affected":
			waits = waits || slept >= sleepPollFloor
		}
	})
	return waits
}

// budgetHolderWait reports a line that waits on a budget holder's process:
// `caffeinate -w`, `wait`, `lsof -p` or `kill -0`. A run's own claim does
// that wait. No broker answering reads as no holder.
func budgetHolderWait(ctx context.Context, command string, d Dialect) bool {
	pids := waitedPIDs(command, d)
	if len(pids) == 0 {
		return false
	}
	s, err := budgetSnapshot(ctx)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(s.Holders, func(h types.MachineClaimant) bool {
		return h.PID > 0 && slices.Contains(pids, strconv.Itoa(h.PID))
	})
}

// waitedPIDs is each pid the processes on a line wait on: `caffeinate -w <pid>`, `wait <pid>`,
// `lsof -p <pid>` and `kill -0 <pid>`.
func waitedPIDs(command string, d Dialect) []string {
	cmds, ok := ParseCommandsDialect(command, d)
	if !ok {
		return nil
	}
	var pids []string
	for _, c := range cmds {
		switch name := path.Base(c.Name); name {
		case "caffeinate", "lsof":
			flag := map[string]string{"caffeinate": "w", "lsof": "p"}[name]
			for i, a := range c.Args {
				if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.HasSuffix(a, flag) && i+1 < len(c.Args) {
					pids = append(pids, strings.Split(c.Args[i+1], ",")...)
				}
			}
		case "wait":
			pids = append(pids, nonFlagArgs(c.Args)...)
		case "kill":
			if slices.Contains(c.Args, "-0") {
				pids = append(pids, nonFlagArgs(c.Args)...)
			}
		}
	}
	return pids
}

// statusFlag reports a status argv carrying any of names, in any spelling Go's flag
// package reads.
func statusFlag(args []string, names ...string) bool {
	return slices.ContainsFunc(args, func(a string) bool {
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		return strings.HasPrefix(a, "-") && slices.Contains(names, name)
	})
}

// eachCall calls fn with every command a parsed line runs, wrappers peeled, beside the
// call it was written as.
func eachCall(f *syntax.File, d Dialect, fn func(*syntax.CallExpr, hint.Invocation)) {
	syntax.Walk(f, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok {
			for _, c := range peelWrappers(literalWords(call.Args), d) {
				fn(call, c)
			}
		}
		return true
	})
}

// refusedRetryWindow is how long after a refusal a rerun of the same command is
// a retry loop. In two days of session records, 106 of 140 consecutive MGS3009
// refusals came back inside it.
const refusedRetryWindow = 60 * time.Second

// refusalCodes are the refusals a retry of the same command cannot clear.
var refusalCodes = []types.DiagnosticCode{types.MachineBudgetExhausted, types.MemoryPressureHigh}

// refusedRun is a magus command this checkout's log recorded refused.
type refusedRun struct {
	argv []string
	at   time.Time
	code types.DiagnosticCode
}

// denyRefusedRetry refuses a magus run repeating a refusal this checkout's log
// holds inside refusedRetryWindow, while it is the command's newest run.
// Nothing freed a seat or memory meanwhile, and MGS3009 already waited as long
// as budget_wait allows: 30m by default, none for a worker's check.
func denyRefusedRetry(ctx context.Context, deps Dependencies, _, command string) denial {
	runs := magusRuns(command)
	if len(runs) == 0 {
		return denial{}
	}
	now := time.Now()
	r, ok := recentRefusal(workspaceRunsDir(hookLocation(ctx, deps).cacheDir), runs, now)
	if !ok {
		return denial{}
	}
	run := strings.Join(append([]string{hint.BinaryName()}, quoteArgs(r.argv)...), " ")
	ago := now.Sub(r.at).Round(time.Second)
	if r.code == types.MemoryPressureHigh {
		next := "Free memory first, then run it once the minute has passed"
		if deps.lease != "" {
			next = cache.LeasedPressureNext(deps.lease)
		}
		return denial{
			Say: fmt.Sprintf("magus workspace: `%s` was refused with %s %s ago: this machine is short of memory. %s.", run, r.code, ago, next),
			Why: denyPressureRetryWhy,
		}
	}
	if deps.lease == "" {
		return denial{
			Say: fmt.Sprintf("magus workspace: `%s` was refused with %s %s ago; the root runs no target: fork a job for it: `%s`.",
				run, r.code, ago, hint.JobFork.String()),
			Why: denyRefusedRetryWhy,
		}
	}
	return denial{
		Say: fmt.Sprintf("magus workspace: `%s` was refused with %s %s ago, and a worker's check never waits for the budget: free a holder, or file the check as owed %q with `%s --stdin`.",
			run, r.code, ago, types.OwedNoSeat, hint.JobExit.With(deps.lease)),
		Why: denyRefusedRetryWhy,
	}
}

var (
	denyRefusedRetryWhy = "An MGS3009 comes after the run waited as long as budget_wait allows (none when it is 0, as every worker's check sets it), and the same command run again inside a minute meets the same holders: one checkout was once refused 63 times in 12 minutes, each retry a turn spent on a look."

	denyPressureRetryWhy = "A refusal frees no memory, and a retry within the minute reads the same failing machine. On 2026-10-31 this state ended in a kernel panic, so an agent looping here spends turns on the machine least able to afford them."
)

// magusRuns is the argv of each magus run or affected on a line.
func magusRuns(command string) [][]string {
	f, err := parseFile(command, DialectBash)
	if err != nil {
		return nil
	}
	var runs [][]string
	eachCall(f, DialectBash, func(_ *syntax.CallExpr, c hint.Invocation) {
		if !isMagusInvocation(c) {
			return
		}
		words := magusSubcommandWords(c.Args)
		if len(words) > 0 && (words[0] == "run" || words[0] == "affected") {
			runs = append(runs, c.Args)
		}
	})
	return runs
}

// commandAttempt is the newest run of one argv in the window, and its refusal if it was
// refused.
type commandAttempt struct {
	started time.Time
	refusal refusedRun
	refused bool
}

// recentRefusal is the newest refusal in runsDir of any of argvs inside
// refusedRetryWindow of now. A later run of the same argv cancels it: one that
// passed, or is still running.
func recentRefusal(runsDir string, argvs [][]string, now time.Time) (refusedRun, bool) {
	cutoff := now.Add(-refusedRetryWindow)
	latest := map[string]commandAttempt{}
	recentRunLogs(runsDir, cutoff, func(started journal.Event, _ time.Time, rest *bufio.Reader) {
		if !slices.ContainsFunc(argvs, func(a []string) bool { return slices.Equal(a, started.Command.Arguments) }) {
			return
		}
		key := strings.Join(started.Command.Arguments, "\x00")
		at := time.UnixMilli(started.Ts)
		if prev, ok := latest[key]; ok && !at.After(prev.started) {
			return
		}
		refusedAt, code, refused := readRefusal(rest)
		latest[key] = commandAttempt{started: at, refusal: refusedRun{argv: started.Command.Arguments, at: refusedAt, code: code}, refused: refused}
	})
	var newest refusedRun
	found := false
	for _, a := range latest {
		if a.refused && !a.refusal.at.Before(cutoff) && (!found || a.refusal.at.After(newest.at)) {
			newest, found = a.refusal, true
		}
	}
	return newest, found
}

// readRefusal reads the rest of one run log for a refused result: when it was
// recorded and its code.
func readRefusal(rest *bufio.Reader) (time.Time, types.DiagnosticCode, bool) {
	for {
		line, err := rest.ReadBytes('\n')
		var ev journal.Event
		// The byte search before decoding keeps a log full of output cheap.
		if slices.ContainsFunc(refusalCodes, func(code types.DiagnosticCode) bool { return bytes.Contains(line, fmt.Appendf(nil, `"%s"`, code)) }) &&
			json.Unmarshal(line, &ev) == nil && ev.Kind == journal.KindResult &&
			slices.Contains(refusalCodes, types.DiagnosticCode(ev.DiagnosticCode)) {
			return time.UnixMilli(ev.Ts), types.DiagnosticCode(ev.DiagnosticCode), true
		}
		if err != nil {
			return time.Time{}, "", false
		}
	}
}

// recentRunLogs calls read for each run log in runsDir last written at or after cutoff,
// with its started event, when it was last written, and the log past that first line. A
// log written before cutoff is skipped unopened, and one that does not open on a started
// event carrying its command is skipped unread, so a directory of thousands stays cheap.
// A runsDir that is "" or unreadable reads nothing.
func recentRunLogs(runsDir string, cutoff time.Time, read func(started journal.Event, modified time.Time, rest *bufio.Reader)) {
	if runsDir == "" {
		return
	}
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			continue
		}
		readRunLog(filepath.Join(runsDir, e.Name()), info.ModTime(), read)
	}
}

// readRunLog is recentRunLogs for one log at path.
func readRunLog(path string, modified time.Time, read func(started journal.Event, modified time.Time, rest *bufio.Reader)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	rd := bufio.NewReader(f)
	first, _ := rd.ReadBytes('\n')
	var started journal.Event
	if json.Unmarshal(first, &started) != nil || started.Kind != journal.KindStarted || started.Command == nil {
		return
	}
	read(started, modified, rd)
}

// quoteArgs quotes each argument for a shell, so a printed command runs as it was recorded.
func quoteArgs(args []string) []string {
	quoted := make([]string, len(args))
	for i, a := range args {
		if q, err := syntax.Quote(a, syntax.LangBash); err == nil {
			a = q
		}
		quoted[i] = a
	}
	return quoted
}

// budgetReadTimeout bounds the broker read a spawn makes, so a wedged broker costs a
// spawn at most this.
const budgetReadTimeout = time.Second

// budgetSnapshot reads this user's machine build budget from its broker. A var so a test
// stands a budget in without a broker.
var budgetSnapshot = func(ctx context.Context) (types.MachineSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, budgetReadTimeout)
	defer cancel()
	st, err := broker.QueryStatus(ctx, broker.DefaultAddr())
	return st.Capacity, err
}

// BudgetFull words a full machine build budget for whoever is about to start more work on
// it: how full, who holds it, and that it frees as those runs end. "" when a slot and
// memory are both free, or the budget is unmeasured. Holders are read oldest first, as
// the broker serves them.
func BudgetFull(s types.MachineSnapshot, now time.Time) string {
	var held []string
	if s.BudgetSlots > 0 && s.HeldSlots >= s.BudgetSlots {
		held = append(held, fmt.Sprintf("slots %d/%d", s.HeldSlots, s.BudgetSlots))
	}
	if s.BudgetMB > 0 && s.HeldMB >= s.BudgetMB {
		held = append(held, fmt.Sprintf("mem %s/%s", types.FormatMB(s.HeldMB), types.FormatMB(s.BudgetMB)))
	}
	if len(held) == 0 {
		return ""
	}
	text := fmt.Sprintf("the build budget is full (%s)", strings.Join(held, ", "))
	if len(s.Holders) > 0 {
		h := s.Holders[0]
		text += fmt.Sprintf(", held by %d run(s); the oldest, %s %s", len(s.Holders), h.Target, cmp.Or(h.Project, "."))
		if h.Dir != "" {
			text += " in " + h.Dir
		}
		if !h.Since.IsZero() {
			text += ", has held its seat " + now.Sub(h.Since).Round(time.Second).String()
		}
	}
	return text + ". A seat frees as each run ends: hold this worker until the completion notice of work you already started arrives, since a worker whose check finds no seat reports and stops."
}

// adviseBudgetFull is the advisory for an orchestrator spawning into a full build budget,
// "" when the budget has room or no broker answers.
func adviseBudgetFull(ctx context.Context, now time.Time) advice {
	s, err := budgetSnapshot(ctx)
	if err != nil {
		return advice{}
	}
	if note := BudgetFull(s, now); note != "" {
		return advice{Say: "magus workspace: " + note, Why: budgetFullWhy}
	}
	return advice{}
}

// sleepPollFires reports a line that sleeps at least sleepPollFloor and then reads a status
// once: a poll whose loop is the caller's own next call. A loop is busy-wait's.
func sleepPollFires(cmds []hint.Invocation) bool {
	slept := false
	for _, c := range cmds {
		if d, ok := sleepDuration(c); ok && d >= sleepPollFloor {
			slept = true
			continue
		}
		if slept && statusProbe(c) {
			return true
		}
	}
	return false
}

// waitBound is the --timeout for a backgrounded run of target. Measured, it is
// took plus half again, rounded up to a minute: check-timeout's margin.
// Unmeasured, it is ceiling, and note says it is no measurement.
func waitBound(target string, took time.Duration, measured bool, ceiling time.Duration) (bound time.Duration, note string) {
	if !measured {
		return ceiling, fmt.Sprintf("nothing recorded here measures `%s` on these projects, so the bound is background-unbounded's %s ceiling, not a measurement", target, formatLimit(ceiling))
	}
	_, bound = checkMargin(took)
	return bound, fmt.Sprintf("`%s` last ran %s here, and %s leaves half again for a cold cache or a loaded machine", target, took.Round(time.Second), formatLimit(bound))
}

// lastRunHere is the newest real run of r's target in root's session store,
// the longest over several projects. One project with no run leaves the line
// unmeasured. So does `affected`: no one project's run prices the diff's set.
func lastRunHere(root string, r targetRun) (time.Duration, bool) {
	if root == "" || r.verb != "run" {
		return 0, false
	}
	dir, err := sessions.Dir(root)
	if err != nil {
		return 0, false
	}
	projects := r.projects
	if len(projects) == 0 {
		projects = []string{"."}
	}
	var last time.Duration
	for _, p := range projects {
		d, ok := sessions.LastRunDuration(dir, p, r.target)
		if !ok {
			return 0, false
		}
		last = max(last, d)
	}
	return last, true
}

// waitedRun is the one target run a line makes, spelled as written. False for
// none, for several, and for a run the served line could not repeat. That is
// a word the shell computes, or an environment assigned beside it.
func waitedRun(command string, d Dialect) ([]string, targetRun, bool) {
	f, err := parseFile(command, d)
	if err != nil {
		return nil, targetRun{}, false
	}
	var argv []string
	var r targetRun
	found, repeatable := 0, false
	syntax.Walk(f, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok {
			return true
		}
		words, ok := magusUnder(literalWords(call.Args), d)
		if !ok {
			return true
		}
		tr, ok := parseTargetRun(hint.Invocation{Name: words[0], Args: words[1:]})
		if !ok {
			return true
		}
		argv, r = words, tr
		found++
		repeatable = len(call.Assigns) == 0 && !slices.ContainsFunc(call.Args, func(w *syntax.Word) bool { return !readsAsWritten(w.Parts) })
		return true
	})
	return argv, r, found == 1 && repeatable
}

// waitRemedy backgrounds a waiting line's target run under job exec. A line
// waitedRun declines gets an empty lead, which withRemedy reads as no remedy.
func waitRemedy(deps Dependencies, rule denyRuleName, command string, d Dialect) (string, types.NextStep) {
	run, r, ok := waitedRun(command, d)
	if !ok {
		return "", types.NextStep{}
	}
	took, measured := lastRunHere(deps.scope.root, r)
	bound, note := waitBound(r.target, took, measured, configuredBackgroundLimits(deps).ceiling)
	argv := append([]string{run[0], "job", "exec", "--timeout", formatLimit(bound), "--"}, run...)
	next := hint.NextForDenyRemedy(string(rule), argv,
		"run it in the background: the host announces it when it ends, and job exec stops it at the bound.")
	return fmt.Sprintf("this line polls for a run the host announces when it ends; run it in the background and wait for its notice: %s.", note), next
}

// sleepDuration is how long one sleep invocation waits: GNU's suffixed operands summed, a
// bare number as seconds. false for anything else, including an operand it cannot read.
func sleepDuration(c hint.Invocation) (time.Duration, bool) {
	if path.Base(c.Name) != "sleep" {
		return sleepForm(c)
	}
	if len(c.Args) == 0 {
		return 0, false
	}
	var total time.Duration
	for _, a := range c.Args {
		unit := time.Second
		switch {
		case strings.HasSuffix(a, "s"):
			a = strings.TrimSuffix(a, "s")
		case strings.HasSuffix(a, "m"):
			a, unit = strings.TrimSuffix(a, "m"), time.Minute
		case strings.HasSuffix(a, "h"):
			a, unit = strings.TrimSuffix(a, "h"), time.Hour
		case strings.HasSuffix(a, "d"):
			a, unit = strings.TrimSuffix(a, "d"), 24*time.Hour
		}
		n, err := strconv.ParseFloat(a, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		total += time.Duration(n * float64(unit))
	}
	return total, true
}

// isSleeper reports a command whose job is to wait: `sleep`, or any spelling sleepForm
// reads, whether or not its duration is readable.
func isSleeper(c hint.Invocation) bool {
	if path.Base(c.Name) == "sleep" {
		return true
	}
	_, ok := sleepForm(c)
	return ok
}

// runsSleeper reports whether body runs a sleeper anywhere, nested commands included.
func runsSleeper(body []*syntax.Stmt, d Dialect) bool {
	found := false
	for _, stmt := range body {
		syntax.Walk(stmt, func(n syntax.Node) bool {
			if call, ok := n.(*syntax.CallExpr); ok && slices.ContainsFunc(peelWrappers(literalWords(call.Args), d), isSleeper) {
				found = true
			}
			return !found
		})
	}
	return found
}

// oneLinerSleep is how one interpreter's inline script sleeps: the flags that carry the
// script, and each spelling of a sleep in it.
type oneLinerSleep struct {
	flags    string
	patterns []oneLinerPattern
}

// oneLinerPattern matches a sleep whose first group is a number of unit.
type oneLinerPattern struct {
	re   *regexp.Regexp
	unit time.Duration
}

var (
	sleepSeconds = regexp.MustCompile(`\bsleep\s*\(?\s*([0-9.]+)`)
	sleepCall    = regexp.MustCompile(`\bsleep\s*\(\s*([0-9.]+)`)

	oneLinerSleeps = map[string]oneLinerSleep{
		"perl": {"eE", []oneLinerPattern{
			{regexp.MustCompile(`select\s*\(\s*undef\s*,\s*undef\s*,\s*undef\s*,\s*([^)\s]+)\s*\)`), time.Second},
			{sleepSeconds, time.Second},
			{regexp.MustCompile(`\busleep\s*\(?\s*([0-9.]+)`), time.Microsecond},
		}},
		"ruby":    {"e", []oneLinerPattern{{sleepSeconds, time.Second}}},
		"python":  {"c", []oneLinerPattern{{sleepCall, time.Second}}},
		"python3": {"c", []oneLinerPattern{{sleepCall, time.Second}}},
		"node": {"ep", []oneLinerPattern{
			{regexp.MustCompile(`setTimeout\s*\([^;]*?,\s*([0-9.]+)\s*\)`), time.Millisecond},
			{regexp.MustCompile(`Atomics\.wait\s*\([^;]*?,\s*([0-9.]+)\s*\)`), time.Millisecond},
		}},
	}
)

// sleepForm is how long a command that waits without being `sleep` waits. A
// duration not stated as a number reads as sleepPollFloor. A wait on a pid is
// not a sleep: it is refused only when a run holding a seat has that pid.
// `timeout N sleep M` needs no spelling: timeout is peeled to the sleep it runs.
func sleepForm(c hint.Invocation) (time.Duration, bool) {
	name := path.Base(c.Name)
	switch name {
	case "caffeinate":
		if hasFlag(c.Args, 'w', "") {
			return 0, false
		}
		return flagSeconds(c.Args, "-t")
	case "read":
		return flagSeconds(c.Args, "-t")
	}
	spec, ok := oneLinerSleeps[name]
	if !ok {
		return 0, false
	}
	script, ok := inlineScript(c.Args, spec.flags)
	if !ok {
		return 0, false
	}
	for _, p := range spec.patterns {
		if m := p.re.FindStringSubmatch(script); m != nil {
			n, err := strconv.ParseFloat(m[1], 64)
			if err != nil || n < 0 {
				return sleepPollFloor, true
			}
			return time.Duration(n * float64(p.unit)), true
		}
	}
	return 0, false
}

// flagSeconds is the seconds the word after flag carries, sleepPollFloor when that word is
// not a number, and false when the flag is absent.
func flagSeconds(args []string, flag string) (time.Duration, bool) {
	i := slices.Index(args, flag)
	if i < 0 {
		return 0, false
	}
	if i+1 < len(args) {
		if n, err := strconv.ParseFloat(args[i+1], 64); err == nil && n >= 0 {
			return time.Duration(n * float64(time.Second)), true
		}
	}
	return sleepPollFloor, true
}

// inlineScript is the script an interpreter's argv carries after one of flags (last in a
// short bundle, as in `-we`) or after `--eval`.
func inlineScript(args []string, flags string) (string, bool) {
	for i, a := range args {
		if a == "--eval" && i+1 < len(args) {
			return args[i+1], true
		}
		if script, ok := strings.CutPrefix(a, "--eval="); ok {
			return script, true
		}
		if len(a) >= 2 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(flags, rune(a[len(a)-1])) && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// statusProbe reports a one-shot read of something running elsewhere: a process, the pool
// or a job row, or a CI run or pull request's checks. A watch is not a probe, since it
// waits for the change itself.
func statusProbe(c hint.Invocation) bool {
	if slices.ContainsFunc(c.Args, func(a string) bool { return a == "--watch" || strings.HasPrefix(a, "--watch=") || a == "-w" }) {
		return false
	}
	if path.Base(c.Name) == "gh" && len(c.Args) >= 2 {
		switch c.Args[0] {
		case "pr":
			return slices.Contains([]string{"view", "checks", "status"}, c.Args[1])
		case "run":
			return slices.Contains([]string{"view", "list", "ls"}, c.Args[1])
		}
		return false
	}
	return probesForeignProcess(c)
}

// sleepTimerLine is the longest sleep on a line whose only work is sleeping, printing and
// reading a file once, false for a line that does anything else or never sleeps. A followed
// tail never ends, so it is endlessWatch's.
func sleepTimerLine(command string, d Dialect) (time.Duration, bool) {
	cmds, ok := ParseCommandsDialect(command, d)
	if !ok {
		return 0, false
	}
	var longest time.Duration
	slept := false
	for _, c := range cmds {
		if dur, ok := sleepDuration(c); ok {
			slept, longest = true, max(longest, dur)
			continue
		}
		switch path.Base(c.Name) {
		case "echo", "printf", "true", "date", ":", "cat", "head", "wc", "ls":
			continue
		case "tail":
			if !tailFollows(c) {
				continue
			}
		}
		return 0, false
	}
	return longest, slept
}

// spawnWaiterStartRe is a brief or title that opens on the wait itself.
var spawnWaiterStartRe = regexp.MustCompile(`(?i)^\W*(?:wait|poll|monitor|watch|babysit)(?:s|ing)?\b`)

// spawnWaiterPhraseRe is a wait on other agents' work, named as such.
var spawnWaiterPhraseRe = regexp.MustCompile(`(?i)\b(?:wait|poll|watch)\s+(?:for|on|until)\s+(?:the\s+|another\s+|other\s+|each\s+|every\s+|all\s+)?(?:\S+\s+){0,2}?(?:agent|worker|job|subagent|sibling)s?\b`)

// spawnWaiterHead is how much of a brief is read for its purpose: the opening, where a
// brief says what the child is for, and not the instructions after it.
const spawnWaiterHead = 300

// spawnWaitsFires reports a spawn whose title or brief opening says the child exists to wait.
func spawnWaitsFires(title, brief string) bool {
	head := brief
	if len(head) > spawnWaiterHead {
		head = head[:spawnWaiterHead]
	}
	for _, text := range []string{title, head} {
		if spawnWaiterStartRe.MatchString(text) || spawnWaiterPhraseRe.MatchString(text) {
			return true
		}
	}
	return false
}

// spawnNestedWordRe is a waiting word anywhere in a title: looser than spawnWaitsFires,
// since it only advises.
var spawnNestedWordRe = regexp.MustCompile(`(?i)\b(?:wait|poll|monitor|watch|babysit)(?:s|es|ing|er|ers)?\b`)

func denySpawnWaiter() denial {
	return denial{
		Say: "this child only waits, and the host tells a parent when each child ends: return, and the notice reaches your parent.",
		Why: "A waiter spends a context and a turn per look on work that announces itself, and when the work fails early it waits on until it times out. " +
			"A child sent to watch another child duplicates the notice the parent already gets.",
	}
}

// spawnDuplicate is the row a spawn's title names when an earlier spawn in this session was
// already attributed to it, the host has not said that worker stopped (on stops), and the row
// is still being worked. A running row alone is not enough: a row stays running after its
// worker dies, and only this session's own record says a worker of its own holds it.
func spawnDuplicate(ctx context.Context, at location, facts, stops hint.Gate, title string) (types.Job, bool) {
	row, ok := spawnTitleJob(ctx, at, title)
	if !ok || !row.State.Editing() {
		return types.Job{}, false
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return types.Job{}, false
	}
	return row, liveSpawnedJob(facts, stops, rows, row.ID)
}

func denySpawnDuplicate(row types.Job) denial {
	return denial{
		Say: fmt.Sprintf("job %s already has a worker (%s); continue that worker with a message, or stop it before spawning another: `%s` names it.", row.ID, string(row.State), hint.DescribeJob.With(row.ID)),
		Why: "Two workers on one job write the same paths from two contexts, and each reports a result the other never saw. " +
			"A row stays running after its worker dies until something reaps it, so a stuck row is ended first, never worked around with a second worker.",
	}
}

func adviseSpawnNestedWaiter() advice {
	return advice{
		Say: "magus workspace: a worker spawning a helper to wait or watch: return to your parent, which is told when each worker ends.",
		Why: "Waiting belongs to whoever spawned the work: the host tells a parent when its child ends, and a backgrounded run announces its own end. " +
			"A helper spawned only to wait stacks a third context on the notice the first two already get.",
	}
}
