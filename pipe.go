package magus

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gofrs/flock"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/file/record"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/pipe"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/sys/pid"
	"github.com/egladman/magus/types"
)

// MachineClaims is what a run of targets, under charms and with extraArgs forwarded,
// would ask the machine budget for: one claim per step the run admits, sized as the run
// sizes it. A gating stage waits on these for the run it feeds (see `magus status --wait`).
//
// The steps are the selected targets and, through each skip_cache composer, the
// same-project ctx.needs members it lends claims of their own (types.MembersClaimTheirOwn),
// transitively. Every other member runs under its composer's seat and is already folded
// into that claim.
func (m *Magus) MachineClaims(ctx context.Context, targets []types.Target, charms, extraArgs []string) []types.MachineClaim {
	size := newClaimSizer(m.measuredRuns(m.loadHistory(ctx)), forecast.NewShape(charms, extraArgs), m.chainReadsCharm)
	capacity := m.limiter().Capacity()
	out := make([]types.MachineClaim, 0, len(targets))
	seen := map[types.TargetRef]bool{}
	var claim func(p *types.Project, target string)
	claim = func(p *types.Project, target string) {
		ref := types.TargetRef{Project: p.Path, Target: target}
		if seen[ref] {
			return
		}
		seen[ref] = true
		step := m.buildStep(p, target)
		if size != nil {
			m.sizeClaim(&step, p, target, size)
		}
		slots := max(step.Slots, 1)
		if capacity > 0 {
			slots = min(slots, capacity)
		}
		out = append(out, types.MachineClaim{
			Project: p.Path, Target: target, DeclaredBy: step.MemoryDeclaredBy,
			MemoryMB: step.MemoryMB, Profile: step.MemoryProfile, Sizing: step.MemorySizing, Slots: slots,
		})
		if !types.MembersClaimTheirOwn(p, target) {
			return
		}
		for _, member := range p.TargetChains[target] {
			if member.Project == "" {
				claim(p, member.Target)
			}
		}
	}
	for _, t := range targets {
		if p := m.Get(t.Path); p != nil {
			claim(p, t.Name)
		}
	}
	return out
}

// withStdio hands the locker its invocation's standard streams. See pipe.Stdio.
func withStdio(s *pipe.Stdio) lockerOption { return func(l *projectLocker) { l.stdio = s } }

// gated reports whether procID holds a pipe gate (see pipe.Gate). A gate left by a dead
// process is swept.
func (l *projectLocker) gated(procID int) bool {
	path := pipe.GatePath(l.pipeDir(), procID)
	if _, err := os.Stat(path); err != nil {
		return false
	}
	if lockIsHeld(path) {
		return true
	}
	_ = os.Remove(path)
	return false
}

const (
	holdsSuffix    = ".holds"
	pipeWaitSuffix = ".wait"
	// pipeWaitQuietFor is how long a wait on an upstream that has not yet settled its
	// lock set goes unannounced.
	pipeWaitQuietFor = time.Second
)

// holdsRecord is a run's complete lock set, published once every lock is taken. The
// stage downstream of it reads it to learn which projects it will not get.
//
// It is paired with a flock the publisher holds for as long as the locks, so a record
// left by a killed publisher reads as absent rather than as a live claim.
type holdsRecord struct {
	PID      int    `record:"pid"`
	Inv      string `record:"invocation,omitempty"`
	Projects string `record:"projects"` // newline-separated, "." for the root
}

// pipeWaitRecord names a run waiting on its upstream, for `magus status`.
type pipeWaitRecord struct {
	PID             int       `record:"pid"`
	Command         string    `record:"command"`
	Dir             string    `record:"dir"`
	UpstreamPID     int       `record:"upstream_pid"`
	UpstreamCommand string    `record:"upstream_command,omitempty"`
	Started         time.Time `record:"started"`
}

var holdsSeq atomic.Int64

func (l *projectLocker) pipeDir() string { return filepath.Join(l.dir, pipe.DirName) }

// publishHolds records paths as this invocation's complete lock set, when its stdout is
// a pipe another stage may be reading, and returns the retraction. Best-effort: a stage
// downstream that cannot read the record waits for this one to finish instead.
func (l *projectLocker) publishHolds(ctx context.Context, paths []string) func() {
	if l.stdio == nil || !pipe.IsPipe(l.stdio.Stdout) {
		return func() {}
	}
	dir := l.pipeDir()
	if os.MkdirAll(dir, 0o755) != nil {
		return func() {}
	}
	base := filepath.Join(dir, fmt.Sprintf("%d-%d%s", os.Getpid(), holdsSeq.Add(1), holdsSuffix))
	fl := flock.New(base + ".lock")
	if got, err := fl.TryLock(); err != nil || !got {
		return func() {}
	}
	rec := holdsRecord{PID: os.Getpid(), Inv: journal.InvocationIDFromContext(ctx), Projects: strings.Join(canonicalProjects(paths), "\n")}
	if record.Write(base, rec) != nil {
		_ = fl.Unlock()
		_ = os.Remove(base + ".lock")
		return func() {}
	}
	// The record goes before the flock, so no reader ever sees a live publisher's record
	// with its flock free.
	return func() {
		_ = record.Remove(base)
		_ = fl.Unlock()
		_ = os.Remove(base + ".lock")
	}
}

// settledProjects returns the projects pid has published as its complete lock set, and
// false when it has published none that is live: it has not finished taking its locks,
// has released them, or never takes any. A dead publisher's record is swept.
func (l *projectLocker) settledProjects(procID int) ([]string, bool) {
	matches, _ := filepath.Glob(filepath.Join(l.pipeDir(), strconv.Itoa(procID)+"-*"+holdsSuffix))
	var out []string
	settled := false
	for _, path := range matches {
		var rec holdsRecord
		if record.Read(path, &rec) != nil || rec.PID != procID {
			continue
		}
		if !lockIsHeld(path + ".lock") {
			_ = record.Remove(path)
			_ = os.Remove(path + ".lock")
			continue
		}
		settled = true
		out = append(out, strings.Split(rec.Projects, "\n")...)
	}
	return out, settled
}

// awaitUpstream holds this run back, before it takes any lock, while a magus upstream of
// it in a shell pipe holds or may yet take a project in paths. See pipe.Stdio.
//
// It returns the relay now feeding stdin, or nil when it did not wait, and every proven
// upstream stage. It never waits on an unrelated process: every upstream is proven from
// the kernel to write this run's stdin, directly or through other magus stages, and to
// run this same executable.
func (l *projectLocker) awaitUpstream(ctx context.Context, paths []string) (*pipe.Spool, []pipe.Stage, error) {
	if l.stdio == nil || l.stdio.Stdin == nil {
		return nil, nil, nil
	}
	ups, err := l.stdio.Upstream(ctx)
	if err != nil {
		return nil, nil, err
	}
	blocking := l.blocking(ups, paths)
	if len(blocking) == 0 {
		return nil, ups, nil
	}
	sp, err := l.waitOut(ctx, ups, blocking, paths)
	if sp != nil {
		l.stdio.KeepSpool(sp)
	}
	return sp, ups, err
}

// waitOut drains stdin while any upstream stage still blocks paths, starting from
// blocking, then relays it.
func (l *projectLocker) waitOut(ctx context.Context, ups []pipe.Stage, blocking []upstreamBlock, paths []string) (*pipe.Spool, error) {
	if l.stdio.ReadsRecords() {
		return nil, l.waitBlocking(ctx, ups, blocking, paths, nil)
	}
	sp, err := pipe.StartSpool(l.stdio.Stdin, l.pipeDir())
	if err != nil {
		return nil, fmt.Errorf("workspace lock: drain stdin while waiting on pid %d: %w", blocking[0].stage.PID, err)
	}
	if err := l.waitBlocking(ctx, ups, blocking, paths, sp); err != nil {
		sp.Abandon()
		return nil, err
	}
	if err := sp.HandOff(l.stdio.Stdin); err != nil {
		sp.Abandon()
		return nil, fmt.Errorf("workspace lock: restore stdin: %w", err)
	}
	return sp, nil
}

// waitBlocking polls until no upstream stage blocks paths. sp, when set, is the spool
// draining stdin meanwhile.
func (l *projectLocker) waitBlocking(ctx context.Context, ups []pipe.Stage, blocking []upstreamBlock, paths []string, sp *pipe.Spool) error {
	retract := func() {}
	defer func() { retract() }()
	announced := false
	start := time.Now()

	t := time.NewTicker(lockPollEvery)
	defer t.Stop()
	for len(blocking) > 0 {
		// An upstream that has yet to settle usually does so within a workspace load, and
		// a line for every streaming pipe would be noise. A real conflict, or a wait that
		// drags, is said at once.
		if !announced && (blocking[0].conflict || time.Since(start) >= pipeWaitQuietFor) {
			l.emitPipeWait(ctx, blocking[0].stage)
			retract = l.writePipeWait(ctx, blocking[0].stage)
			announced = true
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("workspace lock: gave up waiting on pid %d upstream of this run: %w", blocking[0].stage.PID, ctx.Err())
		case <-t.C:
		}
		if sp != nil {
			if err := sp.Failed(); err != nil {
				return fmt.Errorf("workspace lock: drain stdin while waiting on pid %d: %w", blocking[0].stage.PID, err)
			}
		}
		blocking = l.blocking(ups, paths)
	}
	return nil
}

// upstreamBlock is an upstream stage this run still waits for. conflict is set when the
// stage holds a project this run needs, rather than having yet to settle its set.
type upstreamBlock struct {
	stage    pipe.Stage
	conflict bool
}

// blocking returns the upstream stages this run must still wait for, conflicts first:
// those that may take locks or hold a gate, still write their pipe, and have either not
// settled a lock set yet or settled one that overlaps paths. A stage whose settled set is
// disjoint streams alongside; a gate never settles one.
func (l *projectLocker) blocking(ups []pipe.Stage, paths []string) []upstreamBlock {
	want := canonicalProjects(paths)
	var out []upstreamBlock
	for _, u := range ups {
		if (!u.TakesLocks && !l.gated(u.PID)) || !u.Writes.WrittenBy(u.PID) {
			continue
		}
		held, settled := l.settledProjects(u.PID)
		conflict := slices.ContainsFunc(held, func(p string) bool { return slices.Contains(want, p) })
		if settled && !conflict {
			continue
		}
		out = append(out, upstreamBlock{stage: u, conflict: conflict})
	}
	slices.SortStableFunc(out, func(a, b upstreamBlock) int {
		switch {
		case a.conflict == b.conflict:
			return 0
		case a.conflict:
			return -1
		default:
			return 1
		}
	})
	return out
}

// canonicalProjects spells the root one way, so two runs compare project sets exactly.
func canonicalProjects(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			p = "."
		}
		out = append(out, p)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// emitPipeWait states the decision, once: a run that sits silently before taking a lock
// looks hung.
func (l *projectLocker) emitPipeWait(ctx context.Context, up pipe.Stage) {
	if l.rw != nil {
		_ = report.Record(l.rw, report.LockPipeWait{UpstreamPID: up.PID, Command: up.Command()})
		return
	}
	if l.gated(up.PID) {
		fmt.Fprintf(l.out, "magus: waiting for pid %d (%s), upstream of this run in a pipe, to exit before starting.\n", up.PID, up.Command())
	} else {
		fmt.Fprintf(l.out, "magus: waiting for pid %d (%s), upstream of this run in a pipe, to finish with the projects this run needs before taking their locks.\n",
			up.PID, up.Command())
	}
	slog.InfoContext(ctx, "lock.pipe_wait", slog.Int("upstream_pid", up.PID), slog.String("upstream_command", up.Command()))
}

// writePipeWait publishes the wait for `magus status` and returns its retraction.
func (l *projectLocker) writePipeWait(ctx context.Context, up pipe.Stage) func() {
	dir := l.pipeDir()
	if os.MkdirAll(dir, 0o755) != nil {
		return func() {}
	}
	self := l.selfRecord(ctx, time.Now())
	path := filepath.Join(dir, strconv.Itoa(self.PID)+pipeWaitSuffix)
	rec := pipeWaitRecord{
		PID: self.PID, Command: self.Command, Dir: self.Dir,
		UpstreamPID: up.PID, UpstreamCommand: up.Command(), Started: self.Started,
	}
	if record.Write(path, rec) != nil {
		return func() {}
	}
	return func() { _ = record.Remove(path) }
}

// PipeWaits reports every run in this workspace waiting, before taking its locks, on the
// magus upstream of it in a pipe. A record whose waiter has exited is swept.
func (m *Magus) PipeWaits() []types.StatusPipeWait {
	return pipeWaits(resolveCacheDir(m.ws.Root, m.cfg), m.ws.Root)
}

func pipeWaits(cacheDir, workspaceRoot string) []types.StatusPipeWait {
	matches, _ := filepath.Glob(filepath.Join(pipe.Dir(cacheDir, workspaceRoot), "*"+pipeWaitSuffix))
	var out []types.StatusPipeWait
	for _, path := range matches {
		var rec pipeWaitRecord
		if record.Read(path, &rec) != nil || rec.PID == 0 {
			continue
		}
		if !pid.Alive(rec.PID) {
			_ = record.Remove(path)
			continue
		}
		out = append(out, types.StatusPipeWait{
			PID: rec.PID, Command: rec.Command, Dir: rec.Dir,
			UpstreamPID: rec.UpstreamPID, UpstreamCommand: rec.UpstreamCommand, WaitTime: rec.Started,
		})
	}
	slices.SortFunc(out, func(a, b types.StatusPipeWait) int { return a.PID - b.PID })
	return out
}
