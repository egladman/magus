package cache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/sys/mem"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
)

// MachineAdmitter is the budget as a client reaches it: the broker, over the one
// connection this process holds to it.
type MachineAdmitter interface {
	// Claim queues c until it is seated or ctx ends; a claim larger
	// than the whole budget answers at once. parked hears each report.
	Claim(ctx context.Context, c types.MachineClaim, parked func(types.MachineVerdict)) (types.MachineVerdict, error)
	Request(ctx context.Context, c types.MachineClaim) (types.MachineVerdict, error)
	Release(ctx context.Context, id string)
	// Overrun says the granted claim id held more than its memory curve predicted, so
	// the budget charges it its full peak until Release. Best-effort, like Release.
	Overrun(ctx context.Context, id string)
	// HoldGroup ties process group pgid to the claim. The broker stops
	// it on release, or when this process's connection closes.
	HoldGroup(ctx context.Context, claim string, pgid int, command string) (string, error)
	// ReleaseHold returns an error when the broker did not answer and may
	// still hold the group.
	ReleaseHold(ctx context.Context, id string) error
}

// KickingAdmitter is admit with kick called once, on the first claim. No claim
// waits for it: a held step may be the one that would free space.
func KickingAdmitter(admit MachineAdmitter, kick func()) *kickingAdmitter {
	return &kickingAdmitter{MachineAdmitter: admit, kick: sync.OnceFunc(kick)}
}

type kickingAdmitter struct {
	MachineAdmitter
	kick func()
}

func (k *kickingAdmitter) Claim(ctx context.Context, c types.MachineClaim, parked func(types.MachineVerdict)) (types.MachineVerdict, error) {
	k.kick()
	return k.MachineAdmitter.Claim(ctx, c, parked)
}

// machineReleaseTimeout bounds handing a claim back. The release runs in the defer
// that still holds this step's local limiter slot, so it must never outlast a sick
// broker.
//
// A var, not a const, for the same reason as elsewhere in this package: a timing a test
// cannot shorten is a timing that either costs real wall-clock or goes uncovered.
var machineReleaseTimeout = 5 * time.Second

// readPressure is a var so a test can stand in for a really full machine.
var readPressure = mem.ReadPressure

// ExitCodeMachineBusy is the process status a machine-budget refusal asks for: 75,
// EX_TEMPFAIL. It lives here rather than beside the CLI's other exit codes because the
// error is built here and has to state its own code: the server runs an adopted step
// in its own process and reads the code off the error, having lost the Go type.
//
// The workspace lock's own contention error picks the same number for the same reason
// (lockContendedExit). Two independent decisions that agree, not one shared setting:
// coupling them would make a change to either silently move the other.
const ExitCodeMachineBusy = 75

// ExitCodeMachineDeclaration is what a PERMANENT machine refusal asks for: 78, EX_CONFIG.
// Both refusals that carry it name the thing to change (a declaration that exceeds the
// whole budget, or an environment variable a nested magus was started without), so they
// are configuration answers rather than timing ones.
const ExitCodeMachineDeclaration = 78

// ExitCodeBrokerUnavailable is what a step refused under `broker: required` asks for:
// 69, EX_UNAVAILABLE. Apart from 75 so a wrapper retrying a busy host does not also
// retry a host with no arbiter at all.
const ExitCodeBrokerUnavailable = 69

// AdmitMachine claims the machine budget for host work outside Cache.Run:
// a session brief's workspace Inspect and any future hook path that loads the same
// way. It waits as a step does, bounded by wait.
//
// A nil admitter is a no-op grant, matching a Cache with no WithMachineAdmission.
// Zero PID, Dir, Invocation, Ancestors and Slots are filled the way claimMachine
// fills a step's claim, so a caller names the work (Project, Target, MemoryMB) and
// nothing else.
func AdmitMachine(ctx context.Context, admit MachineAdmitter, c types.MachineClaim, required bool, wait time.Duration) (release func(), err error) {
	if c.PID == 0 {
		c.PID = os.Getpid()
	}
	if c.Dir == "" {
		c.Dir = workingDir()
	}
	if c.Invocation == "" {
		c.Invocation = selfInvocation(ctx)
	}
	if c.Ancestors == nil {
		c.Ancestors = ancestorInvocations(ctx)
	}
	if c.Slots == 0 {
		c.Slots = 1
	}
	g := &machineGate{admit: admit, required: required, wait: wait, log: slog.With(attr.Component("magus"))}
	return g.acquire(ctx, c)
}

// machineGate is the client half of admission: it asks the budget, and either proceeds,
// waits on its own run, or refuses.
type machineGate struct {
	admit MachineAdmitter
	// wait bounds a wait on other invocations' claims (budget_wait).
	wait time.Duration
	log  *slog.Logger
	lost sync.Once
	// unread says once that this host's memory state has no reading.
	unread sync.Once
	// required refuses a step the admitter could not answer for, where the default
	// admits it unarbitrated.
	required bool
}

// acquire claims the machine budget for one step and returns the function that frees
// it, or an MGS3009 error naming who holds it.
//
// A step declaring nothing still takes a slot: concurrency is the half every step
// spends. A step kept out only by claims of its own process or its own run waits for
// them, until ctx ends.
// One kept out by another invocation waits up to g.wait, then is
// refused (exit 75).
//
// A broker that dies, or a transport that breaks, admits the step and says so once
// under best-effort; under required it refuses with MGS3022 (exit 69).
// A machine short of memory refuses with MGS3053 (exit 75) before that.
func (g *machineGate) acquire(ctx context.Context, c types.MachineClaim) (func(), error) {
	_, release, err := g.claim(ctx, c)
	return release, err
}

// machineHold is the machine claim one step took, kept so the step can hand it back
// while it waits on members that claim their own and take it again after.
type machineHold struct {
	gate  *machineGate
	claim types.MachineClaim

	mu      sync.Mutex
	id      string
	release func()
	// lent counts the dispatches waiting with the claim handed back. A count, not a flag:
	// the first hands it back and the last takes it again.
	lent int
}

type machineHoldKey struct{}

// withMachineHold marks ctx as running under the claim h, the step's own; nil marks a
// step riding an ancestor's claim, which it cannot hand back.
func withMachineHold(ctx context.Context, h *machineHold) context.Context {
	return context.WithValue(ctx, machineHoldKey{}, h)
}

// current is the id of the claim the step holds now, "" while it is handed back.
func (h *machineHold) current() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.id
}

// close hands the claim back for good, when the step ends.
func (h *machineHold) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.release != nil {
		h.release()
	}
	h.id, h.release = "", nil
}

func (h *machineHold) lend() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lent++
	if h.lent == 1 && h.release != nil {
		h.release()
		h.id, h.release = "", nil
	}
}

// reclaim takes the claim again once the last lent dispatch returns.
// It waits as any claim does.
// It reads no memory: this step is running, and running work is never refused.
func (h *machineHold) reclaim(ctx context.Context) error {
	h.mu.Lock()
	h.lent--
	last := h.lent == 0 && h.release == nil
	h.mu.Unlock()
	if !last {
		return nil
	}
	// Claimed unlocked, so a wait starting meanwhile never blocks behind it; such a wait
	// gets the claim handed straight back.
	id, release, err := h.gate.seat(ctx, h.claim)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lent > 0 || h.release != nil {
		release()
		return nil
	}
	h.id, h.release = id, release
	return nil
}

// LendMachineClaim runs fn, a wait on ctx.needs members, with the step's machine claim
// and limiter slots handed back, then takes the claim again; a refused retake is joined
// to fn's error. Members that are steps claim for themselves while it is lent. A claim
// held across the wait is a seat a member, or a need shared with a sibling, may never
// get (see [Cache.claimMachine]). fn runs on ctx unchanged when ctx holds no claim of
// its own step's.
func LendMachineClaim(ctx context.Context, fn func(context.Context) error) error {
	h, _ := ctx.Value(machineHoldKey{}).(*machineHold)
	if h == nil || h.current() == "" {
		return fn(ctx)
	}
	lent := func(ctx context.Context) error {
		h.lend()
		held := admissionFromContext(ctx)
		held.machineClaim = false
		err := fn(withMachineHold(held.on(ctx), nil))
		return errors.Join(err, h.reclaim(ctx))
	}
	lim := LimiterFromContext(ctx)
	if lim == nil || !SlotHeld(ctx) {
		return lent(ctx)
	}
	return lim.Yield(ctx, func() error { return lent(WithoutSlotHeld(ctx)) })
}

// claim is acquire, also returning the id of the claim granted: "" when nothing
// arbitrates the step. broker: off is that case, and reads no memory either.
func (g *machineGate) claim(ctx context.Context, c types.MachineClaim) (string, func(), error) {
	if g == nil || g.admit == nil {
		return "", func() {}, nil
	}
	if err := g.refuseUnderPressure(ctx, c); err != nil {
		return "", nil, err
	}
	id, release, err := g.seat(ctx, c)
	if err != nil {
		return "", nil, err
	}
	// seat can hold a claim 30 minutes, and a healthy machine can fail in that.
	if err := g.refuseUnderPressure(ctx, c); err != nil {
		release()
		return "", nil, err
	}
	return id, release, nil
}

// refuseUnderPressure is the MGS3053 refusal while the kernel says memory is
// bad. A missing or partial reading is said once. A nested run passes: refusing
// it would fail the running step above it. A wrapper above it runs no step.
func (g *machineGate) refuseUnderPressure(ctx context.Context, c types.MachineClaim) error {
	if types.RunsUnderAStep(c.Ancestors) || spawn.CurrentLevel() > 0 {
		return nil
	}
	reason, err := readPressure(ctx)
	if err != nil {
		g.unread.Do(func() {
			g.log.WarnContext(ctx, "this run's memory reading is missing or partial, so a step may start on a bad machine", attr.Error(err))
		})
	}
	if reason == "" {
		return nil
	}
	return memoryPressureError(c, reason, claimLease(ctx))
}

// seat asks the budget for c, with no reading of memory. It waits in the
// budget's queue, and a wait on other invocations ends at g's bound.
func (g *machineGate) seat(ctx context.Context, c types.MachineClaim) (string, func(), error) {
	if g == nil || g.admit == nil {
		return "", func() {}, nil
	}
	wctx, w := g.newBudgetWait(ctx, c)
	defer w.stop()
	v, err := g.ask(wctx, c, func(v types.MachineVerdict) { w.parked(ctx, v) })
	if err != nil {
		if ctx.Err() != nil {
			return "", nil, fmt.Errorf("machine budget: gave up waiting for %s %s: %w", displayProject(c.Project), c.Target, ctx.Err())
		}
		if w.expired() {
			return g.atBound(ctx, c, w)
		}
		// An admitter that answers with an exit status refused the step itself (its
		// workspace's admission config does not read), which no broker policy excuses.
		if exit := (types.ExitError{}); errors.As(err, &exit) {
			return "", nil, err
		}
		if g.required {
			return "", nil, brokerUnavailableError(c, err)
		}
		return "", g.admitOpen(ctx, err), nil
	}
	switch {
	case v.Granted:
		w.seated(ctx)
		return v.ID, g.releaser(v.ID), nil
	case !v.Fits:
		return "", nil, machineDoesNotFitError(c, v)
	default:
		return "", nil, fmt.Errorf("machine budget: the admitter answered %s %s without seating it or holding the claim until it is seated", displayProject(c.Project), c.Target)
	}
}

// atBound is the one unparked ask a wait gets at its bound: the room may
// have come free as the bound passed. Otherwise it is MGS3009 naming the
// holders now.
func (g *machineGate) atBound(ctx context.Context, c types.MachineClaim, w *budgetWait) (string, func(), error) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), machineReleaseTimeout)
	defer cancel()
	v, err := g.admit.Request(rctx, c)
	switch {
	case err != nil:
		v = w.last()
	case v.Granted:
		w.seated(ctx)
		return v.ID, g.releaser(v.ID), nil
	case !v.Fits:
		return "", nil, machineDoesNotFitError(c, v)
	}
	if w.blind {
		// A nested magus that cannot name its ancestors cannot be excused
		// from its parent's claim, so the refusal names the fix.
		return "", nil, machineBlindError(c, v)
	}
	return "", nil, machineBusyError(c, v, time.Now(), claimLease(ctx), w.waited(), w.bound)
}

// waitBound is how long a wait on other invocations may last, and what
// set that.
type waitBound struct {
	d      time.Duration
	source string
}

// BudgetWaitSource names where budget_wait is set.
const BudgetWaitSource = "budget_wait (--budget-wait, MAGUS_BUDGET_WAIT or magus.yaml)"

// budgetBound is the smaller of wait and the time left before ctx's deadline.
func budgetBound(ctx context.Context, wait time.Duration) waitBound {
	b := waitBound{d: wait, source: "the bound " + BudgetWaitSource + " sets"}
	if dl, ok := ctx.Deadline(); ok && wait > 0 {
		if left := time.Until(dl); left < wait {
			b = waitBound{d: max(left, 0), source: "this run's deadline"}
		}
	}
	return b
}

// budgetWait is one claim's wait in the budget's queue. The bound starts
// the first time another invocation keeps the claim out. A run queued
// behind its own steps waits as long as they take.
type budgetWait struct {
	cancel context.CancelFunc
	gate   *machineGate
	claim  types.MachineClaim
	bound  waitBound
	// blind is a nested run that lost its ancestry. It never queues behind
	// another invocation.
	blind bool

	mu      sync.Mutex
	since   time.Time
	holders string
	verdict types.MachineVerdict
	timer   *time.Timer
	fired   bool
}

func (g *machineGate) newBudgetWait(ctx context.Context, c types.MachineClaim) (context.Context, *budgetWait) {
	wctx, cancel := context.WithCancel(ctx)
	return wctx, &budgetWait{cancel: cancel, gate: g, claim: c, bound: budgetBound(ctx, g.wait), blind: blindToOwnAncestry(ctx)}
}

// parked hears each report of what keeps the claim out. A wait on its own
// run passes in silence. A wait on other invocations says so, and again
// when they change.
func (w *budgetWait) parked(ctx context.Context, v types.MachineVerdict) {
	if v.OwnRun {
		return
	}
	held := describeMachineHolders(v.Holders)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.verdict = v
	if w.fired {
		return
	}
	if w.since.IsZero() {
		w.since, w.holders = time.Now(), held
		if w.bound.d <= 0 || w.blind {
			w.fired = true
			w.cancel()
			return
		}
		w.timer = time.AfterFunc(w.bound.d, w.expire)
		w.say(ctx, fmt.Sprintf("waiting for a seat for %s %s: %s, and %s; %s. It gives up after %s (%s)",
			displayProject(w.claim.Project), w.claim.Target, describeMachineDeclaration(w.claim),
			describeMachineRemaining(v), held, w.bound.d, w.bound.source))
		return
	}
	if held == w.holders {
		return
	}
	w.holders = held
	elapsed := time.Since(w.since)
	w.say(ctx, fmt.Sprintf("still waiting for a seat for %s %s after %s: now %s; it gives up in %s",
		displayProject(w.claim.Project), w.claim.Target, elapsed.Round(time.Second), held,
		max(w.bound.d-elapsed, 0).Round(time.Second)))
}

func (w *budgetWait) expire() {
	w.mu.Lock()
	w.fired = true
	w.mu.Unlock()
	w.cancel()
}

// expired reports a wait its bound ended, not its caller.
func (w *budgetWait) expired() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fired
}

func (w *budgetWait) last() types.MachineVerdict {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.verdict
}

func (w *budgetWait) waited() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.since.IsZero() {
		return 0
	}
	return time.Since(w.since)
}

// seated says how long a wait on other invocations took.
func (w *budgetWait) seated(ctx context.Context) {
	w.mu.Lock()
	since := w.since
	w.mu.Unlock()
	if since.IsZero() {
		return
	}
	w.say(ctx, fmt.Sprintf("seated %s %s after %s", displayProject(w.claim.Project), w.claim.Target,
		time.Since(since).Round(time.Second)))
}

func (w *budgetWait) say(ctx context.Context, msg string) {
	w.gate.log.InfoContext(ctx, msg, attr.Notice("machine budget"))
}

func (w *budgetWait) stop() {
	w.mu.Lock()
	if w.timer != nil {
		w.timer.Stop()
	}
	w.mu.Unlock()
	w.cancel()
}

// ask is one Claim, beating the stall watchdog every lockWaitHeartbeat while the budget
// holds it: the holders may be another invocation of this process or of this job tree,
// whose progress the watchdog does not see, and a queued run must not trip MGS3012.
func (g *machineGate) ask(ctx context.Context, c types.MachineClaim, parked func(types.MachineVerdict)) (types.MachineVerdict, error) {
	done := make(chan struct{})
	beating := make(chan struct{})
	go func() {
		defer close(beating)
		beat := time.NewTicker(lockWaitHeartbeat)
		defer beat.Stop()
		for {
			select {
			case <-done:
				return
			case <-beat.C:
				ProgressFromContext(ctx).Beat()
			}
		}
	}()
	defer func() {
		close(done)
		<-beating
	}()
	return g.admit.Claim(ctx, c, parked)
}

// leaseWeight is the weight of the job a run on ctx acts under, resolved as the run's lease
// is (see job.RunLease), or 0 when it acts under none or the job store cannot be read.
func leaseWeight(ctx context.Context, cacheDir string) float64 {
	stateBase, err := xdg.StateDir()
	if err != nil {
		return 0
	}
	loc := job.Location{CacheDir: cacheDir, StateBase: stateBase, DryRun: true}
	lease, _ := job.RunLease(ctx, loc)
	if lease == "" {
		return 0
	}
	rows, err := job.NewStore(loc).List()
	if err != nil {
		return 0
	}
	if i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == lease }); i >= 0 {
		return rows[i].Weight
	}
	return 0
}

// blindToOwnAncestry reports a run that is inside a magus process tree and cannot say
// which invocations it is under. MAGUS_LEVEL says a magus started this one; an empty
// ancestry says we cannot tell which claims are our parent's.
//
// It asks ancestorInvocations rather than the context, so the two agree on what this run
// knows. Reading ctx alone made every library consumer blind by construction (the
// variable was in the process the whole time), and refused an in-process SDK run against
// its own parent's claim.
func blindToOwnAncestry(ctx context.Context) bool {
	return spawn.CurrentLevel() > 0 && len(ancestorInvocations(ctx)) == 0
}

// releaser hands the claim back on a fresh, BOUNDED context. Fresh because the step's
// own is cancelled by the time a failing run tears down, and a release that skipped
// would leave the machine paying for work that has stopped. Bounded because this runs
// inside the defer that still holds the local limiter slot: an unbounded release
// against a wedged broker would pin that slot for as long as the broker stays wedged,
// turning one sick process into a stalled run.
func (g *machineGate) releaser(id string) func() {
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), machineReleaseTimeout)
		defer cancel()
		g.admit.Release(ctx, id)
	}
}

type overrunWatchKey struct{}

// overrunWatch is the granted claim a step's samples are judged against.
type overrunWatch struct {
	admit   MachineAdmitter
	id      func() string
	profile types.MemoryProfile
}

// withOverrunWatch marks ctx as holding the claim id names, granted with profile. A claim
// with no profile is charged its peak already, so there is nothing to watch.
func withOverrunWatch(ctx context.Context, admit MachineAdmitter, id func() string, profile types.MemoryProfile) context.Context {
	if id() == "" || profile.Runs == 0 {
		return ctx
	}
	return context.WithValue(ctx, overrunWatchKey{}, overrunWatch{admit: admit, id: id, profile: profile})
}

// watchOverrun compares each sample the collector under ctx hears to the claim's
// curve, and the first time one exceeds it tells the admitter, once. The call leaves
// the sampler's goroutine, which every exec in the process shares and a sick broker
// must not stall.
func watchOverrun(ctx context.Context) {
	w, ok := ctx.Value(overrunWatchKey{}).(overrunWatch)
	if !ok {
		return
	}
	var once sync.Once
	types.OnRSSSample(ctx, func(at time.Duration, bytes int64) {
		if bytes <= int64(w.profile.HeldMB(at))<<20 {
			return
		}
		id := w.id()
		if id == "" {
			return
		}
		once.Do(func() {
			go func() {
				octx, cancel := context.WithTimeout(context.WithoutCancel(ctx), machineReleaseTimeout)
				defer cancel()
				w.admit.Overrun(octx, id)
			}()
		})
	})
}

// withStepGroups returns ctx under which every process group spawn.Exec starts rides
// the claim claim names when the group starts.
//
// A claim that reads "" while a group starts is handed back (lent, or the step ended),
// so the group is left unheld without a warning. A broker that refuses a hold warns
// once per step, not once per child.
func withStepGroups(ctx context.Context, admit MachineAdmitter, claim func() string) context.Context {
	if claim() == "" {
		return ctx
	}
	var project, target string
	if h, _ := ctx.Value(machineHoldKey{}).(*machineHold); h != nil {
		project, target = h.claim.Project, h.claim.Target
	}
	var refused sync.Once
	unheld := func(pgid int, command string, err error) {
		refused.Do(func() {
			slog.WarnContext(ctx, UnheldGroupWarning(project, target, pgid, command, err))
		})
	}
	return spawn.WithGroupWatch(ctx, func(pgid int, command string) func() {
		id := claim()
		if id == "" {
			return func() {}
		}
		return holdStepGroup(ctx, admit, id, pgid, command, unheld)
	})
}

// UnheldGroupWarning is the line said once when the broker refuses to tie a step's
// child processes to its claim; pgid and command name the first child refused.
func UnheldGroupWarning(project, target string, pgid int, command string, err error) string {
	return fmt.Sprintf("child processes of %s:%s are not tied to its build claim, so they keep running if magus exits abnormally: the broker refused the hold (%v). First child: pid %d (%s).",
		displayProject(project), target, err, pgid, command)
}

// holdStepGroup hands holder process group pgid, which the step holding claim started,
// so the group ends with the claim: when the claim is released, or when this process
// dies and its connection to the broker closes. Call release once the group's leader has
// exited, before it is reaped: a stop after the reap could reach another group that took
// the id.
//
// The hold runs on a context no sibling's failure cancels: a cancel mid-request could
// leave a hold the broker recorded that this process never learns the id of, and so
// never releases. It is bounded the way a release is, so a sick broker cannot stall the
// step that started the group.
func holdStepGroup(ctx context.Context, holder MachineAdmitter, claim string, pgid int, command string, unheld func(pgid int, command string, err error)) (release func()) {
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), machineReleaseTimeout)
	id, err := holder.HoldGroup(hctx, claim, pgid, command)
	cancel()
	if err != nil {
		unheld(pgid, command, err)
		return func() {}
	}
	return func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), machineReleaseTimeout)
		defer cancel()
		if err := holder.ReleaseHold(rctx, id); err != nil {
			slog.WarnContext(ctx, fmt.Sprintf("the broker may still hold process group %d (%s), which ended", pgid, command),
				attr.Error(err))
		}
	}
}

// admitOpen is the best-effort path: the broker is unreachable, so this run proceeds
// unarbitrated. Said once, because a run whose every step repeats it teaches the
// reader to scroll past the line.
func (g *machineGate) admitOpen(ctx context.Context, err error) func() {
	g.lost.Do(func() {
		if errors.Is(err, types.PeerFromAnotherBuild) {
			g.log.WarnContext(ctx, types.FormatDiagnostic(types.PeerFromAnotherBuild,
				"host capacity is not arbitrated for this run: a broker from another magus build answered (broker: best-effort)"),
				attr.Error(err))
			return
		}
		g.log.WarnContext(ctx, "host capacity is not arbitrated for this run: no broker answered (broker: best-effort)",
			attr.Error(err))
	})
	return func() {}
}

// brokerUnavailableError is the refusal under `broker: required`: nothing answered for
// the host's capacity, and the setting says a step must not start unarbitrated.
// A broker from another build counts as none.
func brokerUnavailableError(c types.MachineClaim, err error) error {
	if errors.Is(err, types.PeerFromAnotherBuild) {
		return types.ExitError{Code: ExitCodeBrokerUnavailable, Err: types.WrapDiagnostic(types.PeerFromAnotherBuild, err,
			"not starting %s %s: a broker from another magus build answered for this host's capacity (%v), and broker: required refuses to run unarbitrated",
			displayProject(c.Project), c.Target, err)}
	}
	return types.ExitError{Code: ExitCodeBrokerUnavailable, Err: types.WrapDiagnostic(types.BrokerUnavailable, err,
		"not starting %s %s: no broker answered for this host's capacity (%v), and broker: required refuses to run unarbitrated. `magus broker status` says whether one is up and where it logs",
		displayProject(c.Project), c.Target, err)}
}

// ancestorInvocations is the invocations this one runs underneath, this one excluded.
//
// The pid check is what makes the tail trustworthy as ours: an invocation that appends
// nothing leaves its PARENT's ref last, and dropping that would put the parent back in
// the competing set.
//
// The CLI and the server stamp ancestry onto ctx at their own entry points; a LIBRARY
// caller does not, and admission is the THIRD entry point to need this: the project
// lock hit it first and fixed it the same way (see acquireLocks). Without the fallback
// a Go test driving magus in-process reads an empty ancestry however deep inside a
// magus process tree it is running, so it cannot be excused from the claim its own
// parent is holding and gets refused by a budget its parent filled.
func ancestorInvocations(ctx context.Context) []string {
	refs := types.InvocationAncestorsFromContext(ctx)
	if len(refs) == 0 {
		// Nothing upstream stamped one, so the environment is the only carrier left.
		// childEnv puts it on every op subprocess, magus or not.
		return spawn.AncestorsFromEnv()
	}
	if mintedHere(refs[len(refs)-1]) {
		return refs[:len(refs)-1]
	}
	return refs
}

// selfInvocation is this invocation's own reference, or "" when the caller minted
// none. An unstamped claim is never excluded as an ancestor: it counts.
func selfInvocation(ctx context.Context) string {
	refs := types.InvocationAncestorsFromContext(ctx)
	if len(refs) > 0 && mintedHere(refs[len(refs)-1]) {
		return refs[len(refs)-1]
	}
	return ""
}

// mintedHere reports whether an invocation ref was appended by THIS process. Refs are
// "<pid>:<id>".
func mintedHere(ref string) bool {
	p, _, ok := strings.Cut(ref, ":")
	return ok && p == strconv.Itoa(os.Getpid())
}

// workingDir names where a holder was started, so a peer naming it points at the
// worktree to go look at rather than at a bare pid.
func workingDir() string {
	dir, _ := os.Getwd()
	return dir
}

// The exit status is per refusal (types.ExitError, and see libs/cmdline/exit.Code for the
// server side). EX_TEMPFAIL says "try again"; a declaration that cannot fit and a nested magus
// that lost its ancestry both answer the same way forever, so a wrapper retrying on 75
// would loop on them.

// machineBusyError is the answer once a wait reaches its bound: other
// invocations still fill the machine. A leased caller files the check owed,
// since a refusal leaves no output ref to cite.
func machineBusyError(c types.MachineClaim, v types.MachineVerdict, now time.Time, lease string, waited time.Duration, bound waitBound) error {
	next := fmt.Sprintf("Raise %s, or free a holder, then run the same command again", BudgetWaitSource)
	if lease != "" {
		next = fmt.Sprintf("Job %s waited its bound: %s", lease, owedFiling(lease))
	}
	return types.ExitError{Code: ExitCodeMachineBusy, Err: types.DiagnosticErrorf(types.MachineBudgetExhausted,
		"not starting %s %s: waited %s for a seat, %s. That is %s; %s, and %s. The broker %s. %s",
		displayProject(c.Project), c.Target, waited.Round(time.Second), describeMachineHolders(v.Holders), bound.source, describeMachineDeclaration(c),
		describeMachineRemaining(v), describeMachinePacking(c, v, now), next)}
}

// owedFiling tells a caller under a job lease how to file a refused check.
func owedFiling(lease string) string {
	return fmt.Sprintf("file this check as owed, not as a ref, with `%s --stdin` and owed %q on its gate evidence",
		hint.JobExit.With(lease), types.OwedNoSeat)
}

// LeasedPressureNext is what a job does after MGS3053. An integration cannot
// file its own checks as owed, and no seat clears this.
func LeasedPressureNext(lease string) string {
	return fmt.Sprintf("Job %s does not wait on memory. An integration owns its checks, so free memory and run this again; any other job should %s",
		lease, owedFiling(lease))
}

// memoryPressureError is no wait: the budget queue waits on declared claims
// and cannot see this.
func memoryPressureError(c types.MachineClaim, reason, lease string) error {
	next := fmt.Sprintf("Free memory, then run the same command again: `%s` shows what magus holds, and this reading counts everything on the machine", hint.Status.String())
	if lease != "" {
		next = LeasedPressureNext(lease)
	}
	return types.ExitError{Code: ExitCodeMachineBusy, Err: types.DiagnosticErrorf(types.MemoryPressureHigh,
		"not starting %s %s: this machine is short of memory right now: %s. Running steps keep going. %s",
		displayProject(c.Project), c.Target, reason, next)}
}

// claimLease is the job lease the refused run acts under, "" when it acts under none or
// the user state dir cannot be read (no checkout record then, so the claim alone answers).
func claimLease(ctx context.Context) string {
	stateBase, _ := xdg.StateDir()
	lease, _ := job.RunLease(ctx, job.Location{StateBase: stateBase, DryRun: true})
	return lease
}

// describeMachinePacking says whether the verdict packed by curve or by peak, and the
// instant the sum passes the budget. The broker sends no instant, so it is found here
// from the holders' curves by the rule the broker applies; a broker that predates
// curves answers Packed false.
func describeMachinePacking(c types.MachineClaim, v types.MachineVerdict, now time.Time) string {
	if !v.Packed {
		if c.MemoryMB <= 0 {
			return "packed by peak"
		}
		how := "packed by peak"
		if c.Profile.Runs > 0 {
			how = "packed by peak, predating memory curves"
		}
		peak := fmt.Sprintf("%s: this claimed the %s peak", how, FormatMB(c.MemoryMB))
		if v.BudgetMB > 0 && v.HeldMB+c.MemoryMB > v.BudgetMB {
			peak += ", which passes the budget now"
		}
		return peak
	}
	budget := NewMachineBudget(v.BudgetMB, 0)
	budget.now = func() time.Time { return now }
	for i, h := range v.Holders {
		budget.claims[strconv.Itoa(i)] = &machineEntry{
			claim:   types.MachineClaim{MemoryMB: h.MemoryMB, Profile: h.Profile},
			started: h.Since,
		}
	}
	at, breached := budget.breach(c, func(string, types.MachineClaim) bool { return false })
	if !breached {
		return "packed by curve"
	}
	if at.Equal(now) {
		return fmt.Sprintf("packed by curve: the claims pass the %s budget together now", FormatMB(v.BudgetMB))
	}
	return fmt.Sprintf("packed by curve: the claims pass the %s budget together at %s, %s from now",
		FormatMB(v.BudgetMB), at.Format(time.TimeOnly), at.Sub(now).Round(time.Second))
}

// machineDoesNotFitError is the refusal no wait can fix: the declaration does not fit
// in the whole budget, so an empty machine would refuse it too.
//
// PERMANENT on purpose, where Buck2 clamps an oversized request to the machine and Bazel
// runs one anyway while nothing else is running. Buck2's permits are an abstract share,
// so capping one moves a scheduling number; a claim here is megabytes of resident memory
// with a measured ground truth in MGS1030, and clamping 26 GiB to 24 GiB does not make
// the process use less. It moves the arbiter from this gate to the OOM killer, which
// picks its victim from the whole machine rather than from the offender. Bazel's idle
// rule ends the same way and costs one thing more: a claim admitted over the budget is a
// floor that freeSlot seats another claim on top of, and the !Fits early return that
// keeps a free seat from being a waiver stops firing for exactly the claims it exists
// for. So over-admission stays where freeSlot left it: at most one claim per stalled
// ancestor, and never a claim larger than the whole budget.
func machineDoesNotFitError(c types.MachineClaim, v types.MachineVerdict) error {
	return types.ExitError{Code: ExitCodeMachineDeclaration, Err: types.DiagnosticErrorf(types.MachineBudgetExhausted,
		"refusing to start %s %s: %s, which does not fit in this machine's whole build budget of %s across %d slots. Waiting would not help; %s",
		displayProject(c.Project), c.Target, describeMachineDeclaration(c),
		FormatMB(v.BudgetMB), v.BudgetSlots, describeMachineOversize(c, v))}
}

// describeMachineOversize says what to change, which is not the same sentence for the two
// axes. A memory figure is a declaration magus has already measured against, so the reader
// is sent to that check rather than to a guess; a slot count is bounded by the cores and
// has nothing to check.
//
// This no longer names a fixed percentage: mem.BudgetMB reserves a share of memory that
// depends on the profile the broker started under (a quarter, under balanced or
// conservative; a small fixed floor, under aggressive), and the budget here carries no
// record of which one applied. Naming one number would be right for one profile and a
// lie for the other.
func describeMachineOversize(c types.MachineClaim, v types.MachineVerdict) string {
	if v.BudgetMB > 0 && c.MemoryMB > v.BudgetMB {
		return fmt.Sprintf("the claim that does not fit is %s %s at %s against a budget of %s, and no wait seats it. That budget is what magus reserved for build work here, not the whole machine; the rest runs the OS, its page cache, and (outside concurrency_profile: aggressive) everything else sharing it. Narrow the run to fewer projects or a lighter target, or run `magus doctor` and read MGS1030, which compares this declaration to the peak memory magus measured: correct the declaration if it has drifted; if it is honest, get a bigger machine.",
			displayProject(c.Project), c.Target, FormatMB(c.MemoryMB), FormatMB(v.BudgetMB))
	}
	return fmt.Sprintf("this machine has %d slots in total, so a step taking %d never fits. Correct the declaration if it is wrong, or run this on a bigger machine.",
		v.BudgetSlots, max(c.Slots, 1))
}

// machineBlindError is the refusal for a nested magus that cannot name its ancestors.
// It says what to fix, because the cause is a magusfile clearing the environment rather
// than anything about the machine.
func machineBlindError(c types.MachineClaim, v types.MachineVerdict) error {
	return types.ExitError{Code: ExitCodeMachineDeclaration, Err: types.DiagnosticErrorf(types.MachineBudgetExhausted,
		"not starting %s %s: this magus runs underneath another one but was started without %s, so it cannot tell its own parent's claim from a stranger's and will not queue behind a run that is waiting for it; %s. %s."+
			" Pass that variable through to nested magus invocations, or let magus set it by not clearing the environment.",
		displayProject(c.Project), c.Target, spawn.AncestorsEnvVar,
		describeMachineRemaining(v), describeMachineHolders(v.Holders))}
}

// describeMachineDeclaration says where the memory and slots came from. A composed
// target is held over a number a target in its chain wrote, and a reader sent to its
// own policy would find nothing to change there.
func describeMachineDeclaration(c types.MachineClaim) string {
	slots := fmt.Sprintf("%d slot", max(c.Slots, 1))
	if max(c.Slots, 1) != 1 {
		slots += "s"
	}
	if how := c.Sizing.Slots.String(); how != "" {
		slots += fmt.Sprintf(" (%s)", how)
	}
	if c.MemoryMB <= 0 {
		return "it takes " + slots
	}
	what := fmt.Sprintf("it claims %s (%s) and takes %s", FormatMB(c.MemoryMB), c.Sizing, slots)
	if c.DeclaredBy != "" && c.DeclaredBy != c.Target {
		what = fmt.Sprintf("it runs %s, which claims %s (%s), and takes %s", c.DeclaredBy, FormatMB(c.MemoryMB), c.Sizing, slots)
	}
	return what
}

func describeMachineRemaining(v types.MachineVerdict) string {
	parts := make([]string, 0, 2)
	if v.BudgetMB > 0 {
		parts = append(parts, fmt.Sprintf("%s of %s is left", FormatMB(max(v.BudgetMB-v.HeldMB, 0)), FormatMB(v.BudgetMB)))
	}
	if v.BudgetSlots > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d slots are free", max(v.BudgetSlots-v.HeldSlots, 0), v.BudgetSlots))
	}
	if len(parts) == 0 {
		return "the budget is unmeasured"
	}
	return strings.Join(parts, " and ")
}

// describeMachineHolders names who is holding the budget, so a refusal is a fact the
// reader can act on rather than a number.
func describeMachineHolders(holders []types.MachineClaimant) string {
	if len(holders) == 0 {
		return "nothing else holds a claim"
	}
	const show = 4
	parts := make([]string, 0, min(len(holders), show))
	for _, h := range holders[:min(len(holders), show)] {
		desc := fmt.Sprintf("pid %d %s %s", h.PID, displayProject(h.Project), h.Target)
		if h.MemoryMB > 0 {
			desc += fmt.Sprintf(" (%s)", FormatMB(h.MemoryMB))
		}
		if h.Dir != "" {
			desc += ", in " + h.Dir
		}
		parts = append(parts, desc)
	}
	list := strings.Join(parts, "; ")
	if len(holders) > show {
		list = fmt.Sprintf("%s and %d more", list, len(holders)-show)
	}
	return "held by " + list
}
