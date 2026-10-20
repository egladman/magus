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
	denySleepPoll = "a sleep before a status read is a poll spread over calls; wait on the event: `" +
		hint.Status.With("--watch=15s") + "` for the project lock, `gh run watch` for CI, or your backgrounded command's completion notice."
	denySleepPollWhy = "Each call spends a turn and the sleep's tool slot to look once, and the next call looks again. " +
		"A watch reads continuously and ends with its subject; a backgrounded command is announced when it ends. Both answer when the state changes, where a sleep answers on its own schedule."

	denySleepTimer    = "a call that only sleeps is a timer; a backgrounded command already announces itself when it ends, so wait for that notice or do other work meanwhile."
	denySleepTimerWhy = "The work's own backgrounded call is the waiter: its notice arrives with the result, where a timer's says only that time passed. " +
		"A timer wakes you on a guess, every timer still pending wakes you again after the work is done, and a foreground sleep past the tool timeout is moved to the background and becomes one."

	denySlotWait = "a worker waits for the build budget only in the call that spends the seat: `" + hint.Status.With("--wait") + " | " + hint.Run.String() +
		" <your check>`. A wait on its own, a watch, or a sleep before a run reserves nothing."
	denySlotWaitWhy = "`status --wait` piped straight into a magus run reserves the seat it sized for that run until the run claims it, so no other worker can take it in between. " +
		"A wait that feeds no run holds nothing, so the run after it can still be refused with MGS3009, and a worker looping on reads and refusals spends turns no one who can free a seat sees."

	budgetFullWhy = "Every worker's check needs a seat in the build budget, and a worker that finds none waits in its check's call until one frees, so spawning into a full budget spends a worker's time waiting. " +
		"The budget is the machine's, shared by every worktree, so it frees only as the runs holding it end."
)

// slotWaitLine reports a line that waits for room in the build budget without reserving
// it: `magus status --wait` that pipes into no magus run, `--watch`, a status read beside
// a sleep or inside a watcher, or a magus run after a sleep of at least sleepPollFloor,
// which retries a run the budget refused. `status --wait | magus run ...` alone is not
// one: the wait holds the seat for the run it feeds.
func slotWaitLine(command string, d Dialect, watcher bool) bool {
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
	paired := seatWaits(f, d)
	waits := false
	eachCall(f, d, func(call *syntax.CallExpr, c hint.Invocation) {
		if !isMagusInvocation(c) {
			return
		}
		words := magusSubcommandWords(c.Args)
		if len(words) == 0 {
			return
		}
		switch words[0] {
		case "status":
			waits = waits || watcher || napped || !paired[call] && statusFlag(c.Args, "wait", "watch", "W")
		case "run", "affected":
			waits = waits || slept >= sleepPollFloor
		}
	})
	return waits
}

// statusFlag reports a status argv carrying any of names, in any spelling Go's flag
// package reads.
func statusFlag(args []string, names ...string) bool {
	return slices.ContainsFunc(args, func(a string) bool {
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		return strings.HasPrefix(a, "-") && slices.Contains(names, name)
	})
}

// seatWaits is each `magus status --wait` on a parsed line whose stdout pipes straight
// into a magus run or affected, and each run it feeds: the wait reserves that run's seat
// until the run claims it. A watch reserves nothing, and neither does a wait whose output
// goes anywhere else.
func seatWaits(f *syntax.File, d Dialect) map[*syntax.CallExpr]bool {
	paired := map[*syntax.CallExpr]bool{}
	syntax.Walk(f, func(n syntax.Node) bool {
		pipe, ok := n.(*syntax.BinaryCmd)
		if !ok || (pipe.Op != syntax.Pipe && pipe.Op != syntax.PipeAll) {
			return true
		}
		wait, run := pipeEnd(pipe.X, true), pipeEnd(pipe.Y, false)
		if wait == nil || run == nil {
			return true
		}
		if magusCallIs(wait, d, func(sub string, args []string) bool { return sub == "status" && statusFlag(args, "wait") }) &&
			magusCallIs(run, d, func(sub string, _ []string) bool { return sub == "run" || sub == "affected" }) {
			paired[wait], paired[run] = true, true
		}
		return true
	})
	return paired
}

// pipeEnd is the call at one end of a pipe's side: its last stage's when last, else its
// first stage's. nil when that stage is not a plain call.
func pipeEnd(s *syntax.Stmt, last bool) *syntax.CallExpr {
	for s != nil {
		switch cmd := s.Cmd.(type) {
		case *syntax.CallExpr:
			return cmd
		case *syntax.BinaryCmd:
			if cmd.Op != syntax.Pipe && cmd.Op != syntax.PipeAll {
				return nil
			}
			s = cmd.X
			if last {
				s = cmd.Y
			}
		default:
			return nil
		}
	}
	return nil
}

// magusCallIs reports whether call, its wrappers peeled, is one magus invocation whose
// subcommand and argv is answers true for.
func magusCallIs(call *syntax.CallExpr, d Dialect, is func(sub string, args []string) bool) bool {
	cs := peelWrappers(literalWords(call.Args), d)
	if len(cs) != 1 || !isMagusInvocation(cs[0]) {
		return false
	}
	words := magusSubcommandWords(cs[0].Args)
	return len(words) > 0 && is(words[0], cs[0].Args)
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

// refusedRetryWindow is how long after an MGS3009 refusal a rerun of the same command is a
// retry loop rather than a fresh attempt: in two days of session records, 106 of 140
// consecutive refusals came back inside it.
const refusedRetryWindow = 60 * time.Second

// refusalCode is how a refusal's code reads inside a run log line, matched before the line
// is decoded so a log full of output costs a byte search per line.
var refusalCode = []byte(`"` + types.MachineBudgetExhausted + `"`)

// refusedRun is a magus command this checkout's run log recorded refused with MGS3009.
type refusedRun struct {
	argv []string
	at   time.Time
}

// denyRefusedRetry refuses a magus run that repeats, argv for argv, one this checkout's run
// log recorded refused with MGS3009 inside refusedRetryWindow, when that refusal is still
// the newest run of the command. Nothing freed a seat on the caller's behalf in between,
// so the rerun is the poll again with the caller's turn as its loop. A run fed by a wait
// (`magus status --wait | magus run ...`) passes, as does any line that runs no target.
// The run log is read only for a line that runs one, and only logs written inside the
// window are opened. Workers and orchestrators are told the same pipe: the wait holds the
// seat for the run it feeds.
func denyRefusedRetry(ctx context.Context, deps Dependencies, _, command string) denial {
	argvs := unwaitedRuns(command)
	if len(argvs) == 0 {
		return denial{}
	}
	now := time.Now()
	r, ok := recentRefusal(workspaceRunsDir(hookLocation(ctx, deps).cacheDir), argvs, now)
	if !ok {
		return denial{}
	}
	run := strings.Join(append([]string{hint.BinaryName()}, quoteArgs(r.argv)...), " ")
	return denial{
		Say: fmt.Sprintf("magus workspace: `%s` was refused with %s %s ago; wait for a seat in the same call: `%s | %s`.",
			run, types.MachineBudgetExhausted, now.Sub(r.at).Round(time.Second), hint.Status.With("--wait"), run),
		Why: denyRefusedRetryWhy + " `status --wait` sizes the piped run's claim, waits until the budget can seat it and reserves that seat for the run, so the run starts as soon as a seat frees instead of on the caller's next turn, and no one takes the seat in between.",
	}
}

var denyRefusedRetryWhy = "A refusal frees no seat, and the same command run again inside a minute meets the same full budget: one checkout was once refused 63 times in 12 minutes, each retry a turn spent on a look."

// unwaitedRuns is the argv of each magus run or affected on a line that no `magus status
// --wait` pipes straight into; nil for a line that runs neither.
func unwaitedRuns(command string) [][]string {
	f, err := parseFile(command, DialectBash)
	if err != nil {
		return nil
	}
	paired := seatWaits(f, DialectBash)
	var argvs [][]string
	eachCall(f, DialectBash, func(call *syntax.CallExpr, c hint.Invocation) {
		if paired[call] || !isMagusInvocation(c) {
			return
		}
		if words := magusSubcommandWords(c.Args); len(words) > 0 && (words[0] == "run" || words[0] == "affected") {
			argvs = append(argvs, c.Args)
		}
	})
	return argvs
}

// commandAttempt is the newest run of one argv in the window, and its refusal if it was
// refused.
type commandAttempt struct {
	started time.Time
	refusal refusedRun
	refused bool
}

// recentRefusal is the newest MGS3009 refusal in runsDir of any of argvs inside
// refusedRetryWindow of now, counting a refusal only while no later run of the same argv
// started: a run that passed, or is still running, after it got its seat.
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
		refusedAt, refused := readRefusal(rest)
		latest[key] = commandAttempt{started: at, refusal: refusedRun{argv: started.Command.Arguments, at: refusedAt}, refused: refused}
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

// readRefusal reads the rest of one run log for a result carrying MGS3009, and when it
// was recorded.
func readRefusal(rest *bufio.Reader) (time.Time, bool) {
	for {
		line, err := rest.ReadBytes('\n')
		var ev journal.Event
		if bytes.Contains(line, refusalCode) && json.Unmarshal(line, &ev) == nil &&
			ev.Kind == journal.KindResult && ev.DiagnosticCode == string(types.MachineBudgetExhausted) {
			return time.UnixMilli(ev.Ts), true
		}
		if err != nil {
			return time.Time{}, false
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
		held = append(held, "slots "+strconv.Itoa(s.HeldSlots)+"/"+strconv.Itoa(s.BudgetSlots))
	}
	if s.BudgetMB > 0 && s.HeldMB >= s.BudgetMB {
		held = append(held, "mem "+cache.FormatMB(s.HeldMB)+"/"+cache.FormatMB(s.BudgetMB))
	}
	if len(held) == 0 {
		return ""
	}
	text := "the build budget is full (" + strings.Join(held, ", ") + ")"
	if len(s.Holders) > 0 {
		h := s.Holders[0]
		text += ", held by " + strconv.Itoa(len(s.Holders)) + " run(s); the oldest, " + h.Target + " " + cmp.Or(h.Project, ".")
		if h.Dir != "" {
			text += " in " + h.Dir
		}
		if !h.Since.IsZero() {
			text += ", has held its seat " + now.Sub(h.Since).Round(time.Second).String()
		}
	}
	return text + ". A seat frees as each run ends: wait for one (`" + hint.Status.With("--watch=15s") +
		"` shows the pool) before starting a worker, since a worker whose check finds no seat reports and stops."
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

// sleepDuration is how long one sleep invocation waits: GNU's suffixed operands summed, a
// bare number as seconds. false for anything else, including an operand it cannot read.
func sleepDuration(c hint.Invocation) (time.Duration, bool) {
	if path.Base(c.Name) != "sleep" || len(c.Args) == 0 {
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
		Say: "this child only waits, and the host tells a parent when each child ends: to wait on a job, run `" + hint.JobWait.With("<job>") + "`.",
		Why: "A waiter spends a context and a turn per look on work that announces itself, and when the work fails early it waits on until it times out. " +
			"A child sent to watch another child duplicates the notice the parent already gets.",
	}
}

// spawnDuplicate is the row a spawn's title names when an earlier spawn in this session was
// already attributed to it and the row is still being worked. A running row alone is not
// enough: a row stays running after its worker dies, and only this session's own record
// says a worker of its own holds it.
func spawnDuplicate(ctx context.Context, at location, facts hint.Gate, title string) (types.Job, bool) {
	row, ok := spawnTitleJob(ctx, at, title)
	if !ok || !row.State.Editing() {
		return types.Job{}, false
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return types.Job{}, false
	}
	return row, spawnedJob(facts, rows, row.ID)
}

func denySpawnDuplicate(row types.Job) denial {
	return denial{
		Say: "job " + row.ID + " already has a worker (" + string(row.State) + "); continue that worker with a message, or stop it before spawning another: `" + hint.DescribeJob.With(row.ID) + "` names it.",
		Why: "Two workers on one job write the same paths from two contexts, and each reports a result the other never saw. " +
			"A row stays running after its worker dies until something reaps it, so a stuck row is ended first, never worked around with a second worker.",
	}
}

func adviseSpawnNestedWaiter() advice {
	return advice{
		Say: "magus workspace: a worker spawning a helper to wait or watch: return to your parent, which is told when each worker ends.",
		Why: "Waiting belongs to whoever spawned the work: the host tells a parent when its child ends, and a root waits on a job with `" + hint.JobWait.With("<job>") + "`. " +
			"A helper spawned only to wait stacks a third context on the notice the first two already get.",
	}
}
