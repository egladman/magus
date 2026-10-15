package guard

import (
	"context"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
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

	denySleepTimer    = "a backgrounded call that only sleeps is a timer, and its notice says only that time passed; background the work itself and wait for its completion notice."
	denySleepTimerWhy = "The host announces a backgrounded command when it ends, so the work's own call is the waiter: its notice arrives with the result. " +
		"A timer wakes you on a guess, and every timer still pending wakes you again after the work is done."
)

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

// sleepTimerLine is the longest sleep on a line whose only work is sleeping and printing,
// false for a line that does anything else or never sleeps.
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
		case "echo", "printf", "true", "date", ":":
			continue
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
