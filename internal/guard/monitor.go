package guard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
)

// The watcher rules. A host's watcher runs a command in the background and wakes the agent
// on each line it prints and again when it expires, so it is a waiter with a schedule of
// its own: armed twice on one subject it wakes the agent twice for one event, and armed on
// work the agent backgrounded it repeats the notice the host already gives. Its command is
// judged as any shell line is; these rules judge the watcher as a whole.

const (
	// A second live watcher on one subject, or one on the agent's own backgrounded work.
	denyRuleMonitorDuplicate denyRuleName = "monitor-duplicate"
	// Too many waiters live at once for one agent.
	denyRuleMonitorPile denyRuleName = "monitor-pile"
)

// The live waiters one agent may hold before the pile is advised against, and refused.
const (
	monitorPileAdvise = 3
	monitorPileDeny   = 5
)

// watchExpiryCap bounds how long a watcher is counted live when its call names a longer
// timeout than a host keeps one armed.
const watchExpiryCap = time.Hour

// watchMarkerPrefix starts the kind of every live-waiter record, which is named
// watch-<agent>-<subject>-<expiry unix seconds> so a listing alone says what is live.
const watchMarkerPrefix = "watch-"

// backgroundMarkerPrefix starts the kind of every record of a command an agent backgrounded,
// whose body is the command.
const backgroundMarkerPrefix = "bg-"

// envelopeWatch reads a watcher's timeout by shape: a timeout in milliseconds beside a
// description and the command to watch, and no ask to run in the background, which a
// watcher always does. false for anything else.
func envelopeWatch(input map[string]any) (time.Duration, bool) {
	ms, ok := input["timeout_ms"].(float64)
	if !ok || ms <= 0 || envelopeString(input, "description") == "" || envelopeString(input, "command") == "" {
		return 0, false
	}
	if _, background := input["run_in_background"]; background {
		return 0, false
	}
	return min(time.Duration(ms)*time.Millisecond, watchExpiryCap), true
}

// waiterCheck is a call that arms a waiter, and whether its caller is a leased worker.
type waiterCheck struct {
	watchCall
	worker bool
}

// waiterCall reads the call as a waiter it arms: a watcher, or a backgrounded command that
// only sleeps or watches without end. false for any other call.
func waiterCall(ctx context.Context, deps Dependencies, facts hint.Gate, env hookRequest, background bool, actingLease, agent, command string, d Dialect, now time.Time) (waiterCheck, bool) {
	switch {
	case env.IsWatch:
		subjects := watchSubjects(command, d)
		return waiterCheck{watchCall: watchCall{
			subject: strings.Join(subjects, "\n"),
			expires: now.Add(env.WatchFor),
			ownWork: watchesOwnWork(facts, agent, subjects),
		}}, true
	case !background:
		return waiterCheck{}, false
	}
	if dur, ok := sleepTimerLine(command, d); ok {
		return waiterCheck{watchCall: watchCall{expires: now.Add(max(dur, time.Second)), timer: true}}, true
	}
	if !endlessWatch(command, d) {
		return waiterCheck{}, false
	}
	me, leased := actingLiveLease(ctx, deps, actingLease)
	return waiterCheck{
		watchCall: watchCall{subject: strings.Join(watchSubjects(command, d), "\n"), expires: now.Add(watchExpiryCap), endless: true},
		worker:    leased && workerRole(me, deps.caller) != "",
	}, true
}

// watchCall is one waiter about to be armed: what it waits on and when it stops counting.
type watchCall struct {
	subject string
	expires time.Time
	// ownWork is a subject the agent itself started in the background: a host task
	// capture, or a process matching a command it backgrounded.
	ownWork bool
	// timer is a backgrounded call that only sleeps, which counts toward the pile and is
	// never a duplicate.
	timer bool
	// endless is a backgrounded call that watches until it is killed, which nothing
	// announces the end of.
	endless bool
}

// endlessWatch reports a line that watches until killed: `magus status --watch`, a
// followed `tail`, or `watch`. Backgrounded, it is a waiter that never ends on its own.
func endlessWatch(command string, d Dialect) bool {
	cmds, ok := ParseCommandsDialect(command, d)
	return ok && slices.ContainsFunc(cmds, func(c hint.Invocation) bool {
		switch path.Base(c.Name) {
		case "watch":
			return true
		case "tail":
			return hasFlag(c.Args, 'f', "follow") || hasFlag(c.Args, 'F', "")
		}
		if !isMagusInvocation(c) {
			return false
		}
		words := magusSubcommandWords(c.Args)
		return len(words) > 0 && words[0] == "status" && slices.ContainsFunc(c.Args, func(a string) bool {
			return a == "--watch" || strings.HasPrefix(a, "--watch=") || a == "-watch" || strings.HasPrefix(a, "-watch=")
		})
	})
}

// watchSubjects normalizes what a watcher's command waits on: the processes, pids, files,
// jobs and remote runs it reads, sorted. A command naming none is its own subject, spaces
// collapsed, so two byte-identical watchers still match.
func watchSubjects(command string, d Dialect) []string {
	cmds, ok := ParseCommandsDialect(command, d)
	if !ok {
		return []string{strings.Join(strings.Fields(command), " ")}
	}
	var out []string
	for _, c := range cmds {
		switch name := path.Base(c.Name); name {
		case "pgrep", "pkill", "pidof":
			if args := nonFlagArgs(c.Args); len(args) > 0 {
				out = append(out, "proc:"+args[len(args)-1])
			}
		case "kill":
			if slices.Contains(c.Args, "-0") {
				for _, a := range nonFlagArgs(c.Args) {
					out = append(out, "pid:"+a)
				}
			}
		case "gh":
			out = append(out, "gh:"+strings.Join(c.Args, " "))
		case "magus":
			switch words := magusSubcommandWords(c.Args); {
			case len(words) >= 3 && (words[0] == "job" && words[1] == "wait" || words[0] == "describe" && words[1] == "job"):
				out = append(out, "job:"+words[2])
			case len(words) >= 1 && words[0] == "status":
				out = append(out, "status")
			}
		default:
			for _, f := range fileOperands(c) {
				if f != "]" && (strings.Contains(f, "/") || strings.Contains(f, ".")) {
					out = append(out, "file:"+f)
				}
			}
		}
	}
	if len(out) == 0 {
		return []string{strings.Join(strings.Fields(command), " ")}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// nonFlagArgs are the arguments that are not flags.
func nonFlagArgs(args []string) []string {
	var out []string
	for _, a := range args {
		if a != "" && !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

// watchesOwnWork reports subjects the agent started itself: a host task capture, or a
// process pattern found in a command it backgrounded this session.
func watchesOwnWork(facts hint.Gate, agent string, subjects []string) bool {
	var backgrounded []string
	for _, s := range subjects {
		if f, ok := strings.CutPrefix(s, "file:"); ok && capturePathRe.MatchString(f) {
			return true
		}
		pattern, ok := strings.CutPrefix(s, "proc:")
		if !ok || pattern == "" {
			continue
		}
		if backgrounded == nil {
			backgrounded = backgroundedCommands(facts, agent)
		}
		if slices.ContainsFunc(backgrounded, func(cmd string) bool { return strings.Contains(cmd, pattern) }) {
			return true
		}
	}
	return false
}

// shortHash names a value in a marker kind: short, filename-safe, and the same for the same
// text.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

// recordBackground files a command the agent backgrounded, for a later watcher of its
// process to be recognized as watching the agent's own work.
func recordBackground(facts hint.Gate, agent, command string) {
	writeAgentMarker(facts, hint.MarkerKind(backgroundMarkerPrefix+shortHash(agent)+"-"+shortHash(command)), []byte(command))
}

func backgroundedCommands(facts hint.Gate, agent string) []string {
	var out []string
	for _, kind := range facts.Kinds(hint.MarkerKind(backgroundMarkerPrefix + shortHash(agent) + "-")) {
		if body, err := os.ReadFile(hint.MarkerPath(facts.CacheDir(), facts.Session(), kind)); err == nil && len(body) > 0 {
			out = append(out, string(body))
		}
	}
	return out
}

// liveWatches are the subject hashes of the agent's waiters that have not expired at now,
// one entry per waiter.
func liveWatches(facts hint.Gate, agent string, now time.Time) []string {
	prefix := watchMarkerPrefix + shortHash(agent) + "-"
	var live []string
	for _, kind := range facts.Kinds(hint.MarkerKind(prefix)) {
		rest := strings.TrimPrefix(string(kind), prefix)
		subject, expiry, ok := strings.Cut(rest, "-")
		if !ok {
			continue
		}
		if secs, err := strconv.ParseInt(expiry, 10, 64); err == nil && time.Unix(secs, 0).After(now) {
			live = append(live, subject)
		}
	}
	return live
}

// subjectKey is the hash a waiter's subjects are recorded under. A timer has no subject,
// so each one is keyed on when it was armed and never matches another.
func (w watchCall) subjectKey(now time.Time) string {
	if w.timer {
		return shortHash("timer:" + strconv.FormatInt(now.UnixNano(), 10))
	}
	return shortHash(w.subject)
}

// recordWatch files w as live until it expires.
func recordWatch(facts hint.Gate, agent string, w watchCall, now time.Time) {
	facts.Touch(hint.MarkerKind(watchMarkerPrefix + shortHash(agent) + "-" + w.subjectKey(now) + "-" + strconv.FormatInt(w.expires.Unix(), 10)))
}

// gradeWatch is the verdict on arming w: refused for a worker when it watches without end,
// refused as a duplicate when a live waiter of the agent's already has its subject or the
// subject is the agent's own backgrounded work, then advised or refused by how many the
// agent already holds. Ungraded.
func gradeWatch(facts hint.Gate, agent string, worker bool, w watchCall, now time.Time) ShellVerdict {
	live := liveWatches(facts, agent, now)
	if worker && w.endless {
		return ShellVerdict{Deny: denyMonitorWorker, Why: denyMonitorDuplicateWhy, Rule: denyRule{Name: denyRuleMonitorDuplicate}}
	}
	if !w.timer {
		if w.ownWork {
			return ShellVerdict{Deny: denyMonitorOwnWork, Why: denyMonitorDuplicateWhy, Rule: denyRule{Name: denyRuleMonitorDuplicate}}
		}
		if slices.Contains(live, w.subjectKey(now)) {
			return ShellVerdict{Deny: denyMonitorDuplicate, Why: denyMonitorDuplicateWhy, Rule: denyRule{Name: denyRuleMonitorDuplicate}}
		}
	}
	switch n := len(live) + 1; {
	case n >= monitorPileDeny:
		return ShellVerdict{Deny: monitorPileText(len(live), true), Why: monitorPileWhy, Rule: denyRule{Name: denyRuleMonitorPile}}
	case n >= monitorPileAdvise:
		return ShellVerdict{Context: monitorPileText(len(live), false), Why: monitorPileWhy, Rule: denyRule{Name: denyRuleMonitorPile}}
	}
	return ShellVerdict{}
}

// gradeWaiter is the graded verdict on arming waiter: gradeWatch's, and for a timer the
// sleep-poll deny, which yields to the pile's own word when the workspace only advises
// against it, since the pile says more: how many are already live.
func gradeWaiter(deps Dependencies, facts hint.Gate, agent string, waiter waiterCheck, now time.Time) ShellVerdict {
	w := deps.grade(gradeWatch(facts, agent, waiter.worker, waiter.watchCall, now))
	if waiter.timer {
		if t := deps.grade(ShellVerdict{Deny: denySleepTimer, Why: denySleepTimerWhy, Rule: denyRule{Name: denyRuleSleepPoll}}); t.Deny != "" || w.Deny == "" && w.Context == "" {
			w = stronger(w, t)
		}
	}
	return w
}

const (
	denyMonitorDuplicate ="a watcher on this subject is already live for you; one per subject is all it takes, and it wakes you when the subject changes."
	denyMonitorOwnWork   = "this watches work you backgrounded, which the host announces when it ends; drop the watcher and wait for that notice."
	denyMonitorWorker    = "a worker's waits are its own runs, and each one's end is announced; drop this endless watch, background the run itself, and wait for its completion notice."
	// One Why for both arms: they are one rule, and the reason is the same.
	denyMonitorDuplicateWhy = "Every watcher wakes you when it prints and again when it expires, so two on one subject wake you twice for one event, and a watcher on your own backgrounded command repeats a notice that already comes. " +
		"Re-arming a watcher that expired is not a duplicate: only a live one counts."
	monitorPileWhy = "Each live waiter, a watcher or a backgrounded sleep, wakes you on its own schedule, and the ones still armed after the work ends wake a finished agent until they expire. " +
		"A waiter counts from when it is armed until its timeout, whether or not its command already exited."
)

// monitorPileText words the pile verdict for an agent already holding live waiters.
func monitorPileText(live int, refuse bool) string {
	remedy := "wait on the work's own completion notice, `" + hint.JobWait.With("<job>") + "` for another agent's job, or `" + hint.Status.With("--watch=15s") + "`"
	if refuse {
		return strconv.Itoa(live) + " waiters are already live for you; arm no more: " + remedy + "."
	}
	return "magus workspace: " + strconv.Itoa(live) + " waiters are already live for you, and each wakes you until it expires; " + remedy + ", and end the turn when nothing else is left."
}
