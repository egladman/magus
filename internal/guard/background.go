package guard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
)

// denyRuleWorkerBackgroundRun names the refusal of a bound worker's magus run the host was
// asked to run in the background.
const denyRuleWorkerBackgroundRun denyRuleName = "worker-background-run"

// advisoryCheckTimeout names the advice to give a worker's magus run a timeout that covers
// the target's last real run.
const advisoryCheckTimeout hint.MarkerKind = "check-timeout"

// shellCall is what the wiring said about how the host runs one shell command. Every field
// is the host's or the wiring's word; a zero foreground limit is a fact nobody declared,
// never "no timeout".
type shellCall struct {
	background bool
	// timeout is the one the call named, zero for none.
	timeout time.Duration
	// foregroundDefault is the host's timeout for a call that names none, and foregroundMax
	// the longest one it allows.
	foregroundDefault, foregroundMax time.Duration
}

// declared reports whether the wiring declared the host's foreground limits.
func (c shellCall) declared() bool {
	return c.foregroundDefault > 0 && c.foregroundMax > 0
}

// foregroundLimit is how long the host waits on this call before moving it to the
// background, and false when the wiring declared no foreground limits to read it from.
func (c shellCall) foregroundLimit() (time.Duration, bool) {
	if !c.declared() {
		return 0, false
	}
	if c.timeout > 0 {
		return min(c.timeout, c.foregroundMax), true
	}
	return c.foregroundDefault, true
}

// workerRun is the target runs one command makes, under the bound worker making them.
type workerRun struct {
	lease types.Job
	runs  []targetRun
	// refusals holds check-timeout to one refusal per target per session when the workspace
	// raised it to deny: the deny gate holds nothing, and a refused run narrowed to one test
	// was refused again. Zero when check-timeout only advises, which its own gate holds.
	refusals hint.Gate
}

// workerRuns is the target runs command makes when a bound worker makes them: the acting
// lease is live and held as a worker, and the caller does not integrate the checkout. False
// otherwise, and for a command that runs no target.
func workerRuns(ctx context.Context, deps Dependencies, actingLease, command string) (workerRun, bool) {
	if actingLease == "" {
		return workerRun{}, false
	}
	cmds, ok := ParseCommands(command)
	if !ok {
		return workerRun{}, false
	}
	var runs []targetRun
	for _, c := range cmds {
		if r, ok := parseTargetRun(c); ok {
			runs = append(runs, r)
		}
	}
	if len(runs) == 0 {
		return workerRun{}, false
	}
	me, ok := actingLiveLease(ctx, deps, actingLease)
	if !ok || actingIntegrator(ctx, deps, me, command) || workerRole(me, deps.caller) == "" {
		return workerRun{}, false
	}
	run := workerRun{lease: me, runs: runs}
	if s, ok := deps.setting(string(advisoryCheckTimeout)); ok && s.Decision == builtin.Deny {
		run.refusals = deps.facts
	}
	return run, true
}

// denyWorkerBackgroundRun refuses a bound worker's magus run the host was asked to run in
// the background, or refuses nothing. A worker that backgrounds its check ends its turn with
// the run pending and parks there. Silent for a call the host runs in the foreground.
//
// Kept out of roleScopedCommandRules: servableRemedy grades a remedy against that list, and
// a remedy is a command line, which never says whether the host backgrounds it.
func denyWorkerBackgroundRun(run workerRun, call shellCall) denial {
	if !call.background || len(run.runs) == 0 {
		return denial{}
	}
	why := "A worker that backgrounds its check parks until the run ends: it ends its turn with the run pending, nothing reports the result to the session that spawned it, and the background tasks pile up. " +
		"Run it in the foreground and never end the turn while a run is pending."
	if call.declared() {
		why += fmt.Sprintf(" A foreground call may wait up to %s; a check that needs longer is one to report to the orchestrator for a narrower check.", call.foregroundMax)
	}
	return denial{
		Say: fmt.Sprintf("magus workspace: lease %s runs `%s` in the background; run it in the foreground with a timeout that covers it.", run.lease.ID, run.runs[0].target),
		Why: why,
	}
}

// adviseCheckTimeout advises a bound worker's magus run whose timeout is shorter than the
// target's last real run allows for, keyed so it speaks once per target per session. It
// names the timeout to pass, capped at the host's foreground maximum, or, when even the
// maximum is short of it, the same run under `magus job exec --timeout`.
//
// Silent for a call the host backgrounds (the deny answers that), for a wiring that declared
// no foreground limits, for a target with no recorded real run, and for a run inside `magus
// job exec --timeout`, which carries its own bound. The session store is read only once
// every other condition holds.
func adviseCheckTimeout(at location, run workerRun, call shellCall) advice {
	limit, ok := call.foregroundLimit()
	if !ok || call.background || at.workspace == "" || len(run.runs) == 0 {
		return advice{}
	}
	dir, err := sessions.Dir(at.workspace)
	if err != nil {
		return advice{}
	}
	for _, r := range run.runs {
		projects := r.projects
		if len(projects) == 0 {
			projects = []string{"."}
		}
		var last time.Duration
		for _, p := range projects {
			if d, ok := sessions.LastRunDuration(dir, p, r.target); ok {
				last = max(last, d)
			}
		}
		// Half again as long, for a cold cache or a loaded machine.
		need := last * 3 / 2
		if last == 0 || limit >= need {
			continue
		}
		why := "A call the host times out moves to the background, and a worker that then ends its turn parks until the run ends, with nobody told the result. " +
			"The duration is the newest real run of this target in the repository's session store; the advised timeout leaves half again as much for a cold cache or a loaded machine. " +
			"It speaks once per target per session, a refusal included, so a narrowed rerun after it goes through."
		key := checkTimeoutKey(r)
		if run.refusals.MarkFired(denyMarker(string(key))) {
			continue
		}
		bound := (need + time.Minute - time.Nanosecond).Truncate(time.Minute)
		timeout := min(bound, call.foregroundMax)
		if last > call.foregroundMax || timeout <= limit {
			return advice{
				Say: fmt.Sprintf("magus workspace: `%s` last ran %s here and needs %s with room, past the %s a foreground call may wait; run it bounded in the background: `%s`.",
					r.target, last.Round(time.Second), need.Round(time.Second), call.foregroundMax, execBounded(r, bound)),
				Why: why + "\n" +
					"Run it in the background and end your turn: its completion notice is the wait. `magus job exec --timeout` stops the run at its deadline, so it is the form a worker may leave in the background and still hear from: the host's completion notice wakes you with the result, and your stop is not refused while it runs. " +
					"A run narrowed after `--` is priced as the whole target: no per-test durations are recorded.",
				Key: key,
			}
		}
		return advice{
			Say: fmt.Sprintf("magus workspace: `%s` last ran %s here and this call waits %s; give it a timeout of %s (%d ms) and wait for it in the foreground.",
				r.target, last.Round(time.Second), limit, timeout, timeout.Milliseconds()),
			Why: why,
			Key: key,
		}
	}
	return advice{}
}

// execBounded is r as the line `magus job exec --timeout` bounds, its forwarded arguments
// kept so a run narrowed with `-- -run` stays narrowed. Not magus run's own --timeout
// (boundedRun): that run still holds the call, and a worker may not background it.
func execBounded(r targetRun, timeout time.Duration) string {
	words := append([]string{"./magus", "job", "exec", "--timeout", timeout.String(), "--", "./magus", r.verb, r.target}, r.projects...)
	if len(r.forwarded) > 0 {
		words = append(append(words, "--"), r.forwarded...)
	}
	for i, w := range words {
		if quoted, err := syntax.Quote(w, syntax.LangBash); err == nil {
			words[i] = quoted
		}
	}
	return strings.Join(words, " ")
}

// checkTimeoutKey holds check-timeout to one firing per session per target and project
// set, hashed so the marker's file name holds no path. No project is the root, as the
// lookup in adviseCheckTimeout reads it.
func checkTimeoutKey(r targetRun) hint.MarkerKind {
	subject := targetName(r.target)
	projects := r.projects
	if len(projects) == 0 {
		projects = []string{"."}
	}
	for _, p := range projects {
		subject += "\x00" + path.Clean(p)
	}
	sum := sha256.Sum256([]byte(subject))
	return advisoryCheckTimeout + "-" + hint.MarkerKind(hex.EncodeToString(sum[:6]))
}
