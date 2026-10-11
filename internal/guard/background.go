package guard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"time"

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

// hostCall is what the wiring said about how the host runs one shell call. Every field is
// the host's or the wiring's word; a zero foreground limit is a fact nobody declared, never
// "no timeout".
type hostCall struct {
	background bool
	// timeout is the one the call named, zero for none.
	timeout time.Duration
	// foregroundDefault is the host's timeout for a call that names none, and foregroundMax
	// the longest one it allows.
	foregroundDefault, foregroundMax time.Duration
}

// limit is how long the host waits on this call before moving it to the background, and
// false when the wiring declared no foreground limits to read it from.
func (c hostCall) limit() (time.Duration, bool) {
	if c.foregroundDefault <= 0 || c.foregroundMax <= 0 {
		return 0, false
	}
	if c.timeout > 0 {
		return min(c.timeout, c.foregroundMax), true
	}
	return c.foregroundDefault, true
}

// workerRuns is the target runs command makes when a bound worker makes them: the acting
// lease is live, does not own the gate, and is held as a worker. Empty otherwise.
func workerRuns(ctx context.Context, deps Dependencies, actingLease, command string) (types.Job, []targetRun) {
	if actingLease == "" {
		return types.Job{}, nil
	}
	cmds, ok := ParseCommands(command)
	if !ok {
		return types.Job{}, nil
	}
	var runs []targetRun
	for _, c := range cmds {
		if r, ok := parseTargetRun(c); ok {
			runs = append(runs, r)
		}
	}
	if len(runs) == 0 {
		return types.Job{}, nil
	}
	me, ok := actingLiveLease(ctx, deps, actingLease)
	if !ok || LeaseOwnsGate(me) || workerRole(me, deps.caller) == "" {
		return types.Job{}, nil
	}
	return me, runs
}

// denyWorkerBackgroundRun refuses a bound worker's magus run the host was asked to run in
// the background, or refuses nothing. A worker that backgrounds its check ends its turn with
// the run pending and parks there. Silent for the orchestrator, a person, and a wiring that
// does not report how the host runs the call.
//
// Kept out of roleScopedCommandRules: servableRemedy grades a remedy against that list, and
// a remedy is a command line, which never says whether the host backgrounds it.
func denyWorkerBackgroundRun(ctx context.Context, deps Dependencies, actingLease, command string, call hostCall) denial {
	if !call.background {
		return denial{}
	}
	me, runs := workerRuns(ctx, deps, actingLease, command)
	if len(runs) == 0 {
		return denial{}
	}
	why := "A worker that backgrounds its check parks until the run ends: it ends its turn with the run pending, nothing reports the result to the session that spawned it, and the background tasks pile up. " +
		"Run it in the foreground and never end the turn while a run is pending."
	if _, ok := call.limit(); ok {
		why += fmt.Sprintf(" A foreground call may wait up to %s; a check that needs longer is one to report to the orchestrator for a narrower check.", call.foregroundMax)
	}
	return denial{
		Say: fmt.Sprintf("magus workspace: lease %s runs `%s` in the background; run it in the foreground with a timeout that covers it.", me.ID, runs[0].target),
		Why: why,
	}
}

// adviseCheckTimeout advises a bound worker's magus run whose timeout is shorter than the
// target's last real run allows for, keyed so it speaks once per target per session. It
// names the timeout to pass, capped at the host's foreground maximum, or says the check
// cannot finish in the foreground when that run alone took longer than the maximum.
//
// Silent for anyone but a bound worker, for a call the host backgrounds (the deny answers
// that), for a wiring that declared no foreground limits, and for a target with no
// recorded real run. The session store is read only once every other condition holds.
func adviseCheckTimeout(ctx context.Context, deps Dependencies, at location, actingLease, command string, call hostCall) (advice, hint.MarkerKind) {
	limit, ok := call.limit()
	if !ok || call.background || at.workspace == "" {
		return advice{}, ""
	}
	_, runs := workerRuns(ctx, deps, actingLease, command)
	if len(runs) == 0 {
		return advice{}, ""
	}
	dir, err := sessions.Dir(at.workspace)
	if err != nil {
		return advice{}, ""
	}
	for _, r := range runs {
		projects := r.projects
		if len(projects) == 0 {
			projects = []string{"."}
		}
		var last time.Duration
		for _, p := range projects {
			if d, ok := sessions.LastRealDuration(dir, p, r.target); ok {
				last = max(last, d)
			}
		}
		// Half again as long, for a cold cache or a loaded machine.
		need := last * 3 / 2
		if last == 0 || limit >= need {
			continue
		}
		key := checkTimeoutKey(r)
		why := "A call the host times out moves to the background, and a worker that then ends its turn parks until the run ends, with nobody told the result. " +
			"The duration is the newest real run of this target in the repository's session store; the advised timeout leaves half again as much for a cold cache or a loaded machine. " +
			"It speaks once per target per session."
		if last > call.foregroundMax {
			return advice{
				Say: fmt.Sprintf("magus workspace: `%s` last ran %s here, past the %s a foreground call may wait; ask the orchestrator for a narrower check.",
					r.target, last.Round(time.Second), call.foregroundMax),
				Why: why,
			}, key
		}
		timeout := min((need + time.Minute - time.Nanosecond).Truncate(time.Minute), call.foregroundMax)
		return advice{
			Say: fmt.Sprintf("magus workspace: `%s` last ran %s here and this call waits %s; give it a timeout of %s (%d ms) and wait for it in the foreground.",
				r.target, last.Round(time.Second), limit, timeout, timeout.Milliseconds()),
			Why: why,
		}, key
	}
	return advice{}, ""
}

// checkTimeoutKey holds check-timeout to one firing per session per target and project
// set, hashed so the marker's file name holds no path.
func checkTimeoutKey(r targetRun) hint.MarkerKind {
	subject := targetName(r.target)
	for _, p := range r.projects {
		subject += "\x00" + path.Clean(p)
	}
	sum := sha256.Sum256([]byte(subject))
	return advisoryCheckTimeout + "-" + hint.MarkerKind(hex.EncodeToString(sum[:6]))
}
