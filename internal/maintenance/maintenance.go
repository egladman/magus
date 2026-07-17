// Package maintenance is the daemon's built-in background maintenance scheduler: a low-key,
// idle-gated loop that runs the rotation and sync JOBS on their configured intervals when the
// daemon is quiet. It submits through the same coalescing proc mechanism a manual trigger uses,
// so a scheduled run and a manual one never double-run; and it reads "last run" from the activity
// trail, so a manual trigger through any path (CLI or RPC) resets the countdown and the schedule
// survives a daemon restart. The daemon (cmd/magus) owns the wiring and passes the runtime
// handles in via Options; the scheduling logic lives here.
package maintenance

import (
	"context"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/jobs"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
)

// checkInterval is how often the scheduler wakes to look for a due job. It is deliberately coarse
// and unrelated to the per-job intervals (hours to days): the tick only decides WHEN to look, and
// a job runs at most once per its configured interval regardless. Keeping it coarse is what makes
// the scheduler low-key - a quiet daemon does a cheap idle check four times an hour, not a poll.
const checkInterval = 15 * time.Minute

// Options are the runtime handles the scheduler needs from the daemon wiring. Socket and Trail are
// funcs because both are set during daemon startup and may not be ready when Start is called; the
// scheduler reads them at each tick.
type Options struct {
	Schedule config.Maintenance // per-job intervals; the only config the scheduler reads
	Socket   func() string      // daemon proc socket address to submit to and query
	Trail    func() string      // activity-trail base dir (where KIND_JOB runs are recorded)
	Version  string             // adoption identity for submitted jobs
}

// scheduledJob is one job the daemon runs on its own: the worker argv to submit, the minimum
// interval since its last run, and the trail action string that identifies its runs (the
// space-joined argv, matching how the daemon records a KIND_JOB event).
type scheduledJob struct {
	argv     []string
	interval time.Duration
	action   string
}

// Start launches the scheduler in a goroutine bound to ctx. It is a no-op when every configured
// interval is zero (all jobs disabled), so a workspace that opts out costs nothing.
func Start(ctx context.Context, opts Options) {
	schedule := buildSchedule(opts.Schedule)
	if len(schedule) == 0 {
		return
	}
	go func() {
		tick := time.NewTicker(checkInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				runDue(ctx, opts, schedule)
			}
		}
	}()
}

// buildSchedule turns the config intervals into the set of jobs to schedule, skipping any with a
// non-positive interval (disabled). It resolves each job's worker argv from the shared registry,
// so the scheduler and the CLI/RPC always submit the identical command.
func buildSchedule(m config.Maintenance) []scheduledJob {
	var out []scheduledJob
	add := func(name string, interval time.Duration) {
		if interval <= 0 {
			return
		}
		if j, ok := jobs.Lookup(name); ok {
			out = append(out, scheduledJob{argv: j.Argv, interval: interval, action: jobs.ActionString(j.Argv)})
		}
	}
	add(jobs.NameRotateActivities, m.RotateActivities)
	add(jobs.NameRotateLogs, m.RotateLogs)
	add(jobs.NameSyncGraph, m.SyncGraph)
	return out
}

// runDue submits every job whose interval has elapsed since its last run, but only when the daemon
// is idle. It stays low-key: a busy or unreachable daemon does nothing and waits for the next
// tick. Submits are best-effort (a job's own success is observed via the trail / Dashboard);
// coalescing means a job already running or freshly submitted is never duplicated.
func runDue(ctx context.Context, opts Options, schedule []scheduledJob) {
	base, addr := opts.Trail(), opts.Socket()
	if base == "" || addr == "" {
		return // trail base or socket not up yet
	}
	st, err := proc.QueryStatus(ctx, addr)
	if err != nil || st == nil || st.Running > 0 {
		return // unreachable or busy: stay out of the way until the next tick
	}
	now := time.Now()
	for _, j := range schedule {
		if isDue(base, j, now) {
			_, _ = proc.SubmitJob(ctx, addr, j.argv, opts.Version)
		}
	}
}

// isDue reports whether j should run now: true if it has never run within the retained trail, or
// if at least its interval has elapsed since its last run (finish time = recorded start plus
// duration). Pure over (base, now) so the schedule decision is testable without a live daemon.
func isDue(base string, j scheduledJob, now time.Time) bool {
	ev, ok := trail.LastRun(base, j.action)
	if !ok {
		return true // never run in the retained trail
	}
	last := time.UnixMilli(ev.Ts + ev.DurMs)
	return now.Sub(last) >= j.interval
}
