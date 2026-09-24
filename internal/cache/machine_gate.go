package cache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	runPkg "github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/types"
)

// MachineAdmitter is the budget as a client reaches it: the broker, over the one
// connection this process holds to it.
type MachineAdmitter interface {
	// Request asks once and answers at once.
	Request(ctx context.Context, c types.MachineClaim) (types.MachineVerdict, error)
	// Wait joins the broker's line and returns once c is seated, turns out never to
	// fit, or ctx ends. onWait hears each change in what keeps c out. An error with ctx
	// still live means the broker went away and took the line with it.
	Wait(ctx context.Context, c types.MachineClaim, onWait func(types.MachineWait)) (types.MachineVerdict, error)
	Release(ctx context.Context, id string)
}

// Why a wait ended early, as its context's cause.
var (
	errWaitExpired = errors.New("waited as long as capacity_wait allows")
	errWaitForeign = errors.New("kept out by another invocation, with no capacity_wait to wait on it")
	errWaitBlind   = errors.New("kept out by another invocation, with no ancestry to tell it from a parent")
)

// maxRequeues bounds how often one step joins a successor broker's line under
// `broker: required` after the broker it waited on went away. A broker that dies this
// often is a broker to report, not to keep trusting.
const maxRequeues = 3

// machineReleaseTimeout bounds handing a claim back. The release runs in the defer
// that still holds this step's local limiter slot, so it must never outlast a sick
// broker.
//
// A var, not a const, for the same reason as elsewhere in this package: a timing a test
// cannot shorten is a timing that either costs real wall-clock or goes uncovered.
var machineReleaseTimeout = 5 * time.Second

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

// machineGate is the client half of admission: it asks the budget, and either proceeds,
// waits in the broker's line, or refuses.
type machineGate struct {
	admit MachineAdmitter
	log   *slog.Logger
	lost  sync.Once
	// required refuses a step the admitter could not answer for, where the default
	// admits it unarbitrated.
	required bool
	// patience is how long a step may wait in line behind other invocations; zero
	// refuses it at once.
	patience time.Duration
	// waiting counts this process's steps in line, so a broker dying under them can
	// say how many it released.
	waiting atomic.Int64
	// released is the one seat a step released by a dead broker runs on under
	// best-effort, so a full host is not handed every waiter at once.
	released chan struct{}
}

func newMachineGate(admit MachineAdmitter, log *slog.Logger, required bool, patience time.Duration) *machineGate {
	return &machineGate{admit: admit, log: log, required: required, patience: max(patience, 0), released: make(chan struct{}, 1)}
}

// acquire claims the machine budget for one step and returns the function that frees
// it, or an MGS3009 error naming who holds it.
//
// A step declaring nothing still takes a slot: concurrency is the half every step
// spends. A step kept out only by claims of its own process or its own run waits in
// line for them, until ctx ends. One kept out by another magus invocation waits for at
// most capacity_wait and is then refused (exit 75); with no capacity_wait, the default,
// it is refused at once.
//
// A broker that cannot be reached, or a transport that breaks, admits the step and says
// so once under best-effort; under required it refuses with MGS3022 (exit 69).
func (g *machineGate) acquire(ctx context.Context, c types.MachineClaim) (func(), error) {
	if g == nil || g.admit == nil {
		return func() {}, nil
	}
	v, err := g.admit.Request(ctx, c)
	if err != nil {
		if g.required {
			return nil, brokerUnavailableError(c, err)
		}
		return g.admitOpen(ctx, err), nil
	}
	blind := blindToOwnAncestry(ctx)
	switch {
	case v.Granted:
		return g.releaser(v.ID), nil
	case !v.Fits:
		return nil, machineDoesNotFitError(c, v)
	case v.OwnRun:
	case blind:
		// A nested magus that cannot name its ancestors cannot be excused from its own
		// parent's claim, so it would wait on the run waiting for it. The refusal names
		// the fix rather than the holders.
		return nil, machineBlindError(c, v)
	case g.patience == 0:
		return nil, machineBusyError(c, v, 0)
	}
	return g.wait(ctx, c, v, blind)
}

// stepWait is one step's wait in line: what it last heard, and whether its bound has
// passed. mu guards all of it, since the bound's timer runs on its own goroutine.
type stepWait struct {
	g       *machineGate
	claim   types.MachineClaim
	blind   bool
	started time.Time

	mu      sync.Mutex
	last    types.MachineWait
	expired bool
	cancel  context.CancelCauseFunc
}

// wait holds a step in the broker's line until it is seated. It holds no claim while it
// waits: the step's whole need is granted at once or not at all, which is why a line of
// such waits cannot deadlock (see MachineBudget.Enqueue).
func (g *machineGate) wait(ctx context.Context, c types.MachineClaim, first types.MachineVerdict, blind bool) (func(), error) {
	g.waiting.Add(1)
	defer g.waiting.Add(-1)
	s := &stepWait{g: g, claim: c, blind: blind, started: time.Now(),
		last: types.MachineWait{Claim: c, OwnRun: first.OwnRun, BlockedBy: first.Holders, Ahead: first.Ahead}}
	stopBeat := beatWhileWaiting(ctx)
	defer stopBeat()
	for requeues := 0; ; requeues++ {
		wctx, cancel := context.WithCancelCause(ctx)
		s.mu.Lock()
		s.cancel = cancel
		s.mu.Unlock()
		var bound *time.Timer
		if g.patience > 0 {
			bound = time.AfterFunc(max(g.patience-time.Since(s.started), 0), s.expire)
		}
		v, err := g.admit.Wait(wctx, c, func(w types.MachineWait) { s.heard(ctx, w) })
		cause := context.Cause(wctx)
		cancel(nil)
		if bound != nil {
			bound.Stop()
		}
		switch {
		case err == nil && v.Granted:
			return g.releaser(v.ID), nil
		case err == nil && !v.Fits:
			return nil, machineDoesNotFitError(c, v)
		case err == nil:
			return nil, fmt.Errorf("machine budget: the broker ended %s %s's wait without an answer", displayProject(c.Project), c.Target)
		case ctx.Err() != nil:
			return nil, fmt.Errorf("machine budget: gave up waiting for %s %s: %w", displayProject(c.Project), c.Target, ctx.Err())
		case errors.Is(cause, errWaitBlind):
			return nil, machineBlindError(c, s.verdict())
		case errors.Is(cause, errWaitExpired), errors.Is(cause, errWaitForeign):
			return nil, machineBusyError(c, s.verdict(), time.Since(s.started))
		}
		// The broker went away and took the line with it.
		if !g.required {
			return g.releasedByDeadBroker(ctx, c, err)
		}
		if requeues == maxRequeues {
			return nil, brokerUnavailableError(c, err)
		}
		g.log.WarnContext(ctx, fmt.Sprintf("magus: the broker went away while %s %s waited for capacity; waiting again on the broker that replaces it, whose line starts over, so its place in line is lost (broker: required)",
			displayProject(c.Project), c.Target))
	}
}

// heard takes one report from the broker: it says the first time the step waits on
// other invocations and each time who that is changes, and ends the wait when its
// bound, or the lack of one, says it must not go on.
func (s *stepWait) heard(ctx context.Context, w types.MachineWait) {
	s.mu.Lock()
	s.last = w
	if !w.OwnRun {
		switch {
		case s.blind:
			s.cancel(errWaitBlind)
		case s.g.patience == 0:
			s.cancel(errWaitForeign)
		case s.expired:
			s.cancel(errWaitExpired)
		}
	}
	s.mu.Unlock()
	// A wait on its own run is how every full run proceeds, and says nothing; a wait on
	// someone else is what a person needs to hear about.
	if !w.OwnRun && s.g.patience > 0 && !s.blind {
		s.g.log.InfoContext(ctx, describeMachineWait(s.claim, w, s.g.patience))
	}
}

// expire ends the wait if what keeps the step out now is another invocation. A wait on
// its own run outlasts the bound: capacity_wait bounds waiting on others.
func (s *stepWait) expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expired = true
	if !s.last.OwnRun {
		s.cancel(errWaitExpired)
	}
}

// verdict is the last report as a refusal would name it.
func (s *stepWait) verdict() types.MachineVerdict {
	s.mu.Lock()
	defer s.mu.Unlock()
	return types.MachineVerdict{Fits: true, Holders: s.last.BlockedBy, Ahead: s.last.Ahead}
}

// releasedByDeadBroker is best-effort's answer to a broker that died under a waiting
// step: run it unarbitrated, and say so. The host it was waiting for was full, which is
// exactly when every waiter proceeding at once does the most harm, so the steps this
// process releases run one at a time on a seat of their own.
func (g *machineGate) releasedByDeadBroker(ctx context.Context, c types.MachineClaim, err error) (func(), error) {
	g.log.WarnContext(ctx, fmt.Sprintf("magus: the broker went away while %d step(s) of this run waited for capacity; %s %s runs unarbitrated, one released step at a time (broker: best-effort)",
		g.waiting.Load(), displayProject(c.Project), c.Target), slog.String("error", err.Error()))
	select {
	case g.released <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("machine budget: gave up waiting for %s %s: %w", displayProject(c.Project), c.Target, ctx.Err())
	}
	var once sync.Once
	return func() { once.Do(func() { <-g.released }) }, nil
}

// beatWhileWaiting keeps the stall watchdog fed while a step waits. The holders may be
// another invocation, whose progress the watchdog cannot see, and a legitimate wait
// must not read as a wedged run (MGS3012).
func beatWhileWaiting(ctx context.Context) (stop func()) {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(lockWaitHeartbeat)
		defer t.Stop()
		for {
			ProgressFromContext(ctx).Beat()
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return func() { close(done) }
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
	return runPkg.CurrentLevel() > 0 && len(ancestorInvocations(ctx)) == 0
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

// admitOpen is the best-effort path: the broker is unreachable, so this run proceeds
// unarbitrated. Said once, because a run whose every step repeats it teaches the
// reader to scroll past the line.
func (g *machineGate) admitOpen(ctx context.Context, err error) func() {
	g.lost.Do(func() {
		g.log.WarnContext(ctx, "magus: host capacity is not arbitrated for this run: no broker answered (broker: best-effort)",
			slog.String("error", err.Error()))
	})
	return func() {}
}

// brokerUnavailableError is the refusal under `broker: required`: nothing answered for
// the host's capacity, and the setting says a step must not start unarbitrated.
func brokerUnavailableError(c types.MachineClaim, err error) error {
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
		return runPkg.AncestorsFromEnv()
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

// The exit status is per refusal (types.ExitError, and see proc.ExitCode for the server
// side). EX_TEMPFAIL says "try again"; a declaration that cannot fit and a nested magus
// that lost its ancestry both answer the same way forever, so a wrapper retrying on 75
// would loop on them.

// machineBusyError is the answer when another magus invocation fills the machine: at
// once without capacity_wait, or once waited has used it up. The same command succeeds
// later.
func machineBusyError(c types.MachineClaim, v types.MachineVerdict, waited time.Duration) error {
	what := "not starting " + displayProject(c.Project) + " " + c.Target
	if waited > 0 {
		what += " after waiting " + roundWait(waited).String()
	}
	why := "this machine's build budget is full"
	if len(v.Ahead) > 0 {
		why = fmt.Sprintf("%d step(s) of other runs have waited longer for this host, and later steps have gone past them as often as the line allows (%s)",
			len(v.Ahead), strings.TrimPrefix(describeMachineHolders(v.Ahead), "held by "))
	}
	state := describeMachineDeclaration(c)
	if v.BudgetMB > 0 || v.BudgetSlots > 0 {
		state += ", and " + describeMachineRemaining(v)
	}
	next := ""
	if waited == 0 {
		next = " `--capacity-wait DURATION` waits in line for it instead."
	}
	return types.ExitError{Code: ExitCodeMachineBusy, Err: types.DiagnosticErrorf(types.MachineBudgetExhausted,
		"%s: %s; %s. %s.%s", what, why, state, describeMachineHolders(v.Holders), next)}
}

// roundWait keeps a wait readable: whole seconds, or tens of milliseconds under one.
func roundWait(d time.Duration) time.Duration {
	if d < time.Second {
		return d.Round(10 * time.Millisecond)
	}
	return d.Round(time.Second)
}

// describeMachineWait is the line a step prints when it starts waiting on other
// invocations, and again each time who that is changes.
func describeMachineWait(c types.MachineClaim, w types.MachineWait, patience time.Duration) string {
	need := fmt.Sprintf("%d slot", max(c.Slots, 1))
	if max(c.Slots, 1) != 1 {
		need += "s"
	}
	if c.MemoryMB > 0 {
		need += ", " + FormatMB(c.MemoryMB)
	}
	held := make([]string, 0, len(w.BlockedBy))
	for _, h := range w.BlockedBy {
		who := fmt.Sprintf("pid %d (%s %s", h.PID, displayProject(h.Project), h.Target)
		if h.Command != "" {
			who += ", " + h.Command
		}
		if h.Dir != "" {
			who += " in " + h.Dir
		}
		who += ")"
		if !h.Since.IsZero() {
			who += " since " + h.Since.Local().Format("15:04")
		}
		held = append(held, who)
	}
	msg := fmt.Sprintf("magus: %s %s is waiting up to %s for %s", displayProject(c.Project), c.Target, patience, need)
	if len(held) > 0 {
		msg += ": held by " + strings.Join(held, "; ")
	}
	if len(w.Ahead) > 0 {
		msg += fmt.Sprintf("; behind %d older waiter(s) of other runs", len(w.Ahead))
	}
	return msg
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
		return "that budget is what magus reserved for build work here, not the whole machine; the rest runs the OS, its page cache, and (outside concurrency_profile: aggressive) everything else sharing it. Run `magus doctor` and read MGS1030, which compares this declaration to the peak memory magus measured: correct the declaration if it has drifted; if it is honest, get a bigger machine."
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
		displayProject(c.Project), c.Target, runPkg.AncestorsEnvVar,
		describeMachineRemaining(v), describeMachineHolders(v.Holders))}
}

// describeMachineDeclaration says where the figure came from. A composed target is
// held over a number a target in its chain wrote, and a reader sent to its own policy
// would find nothing to change there.
func describeMachineDeclaration(c types.MachineClaim) string {
	slots := fmt.Sprintf("%d slot", max(c.Slots, 1))
	if max(c.Slots, 1) != 1 {
		slots += "s"
	}
	if c.MemoryMB <= 0 {
		return "it takes " + slots
	}
	what := fmt.Sprintf("it declares %s and takes %s", FormatMB(c.MemoryMB), slots)
	if c.DeclaredBy != "" && c.DeclaredBy != c.Target {
		what = fmt.Sprintf("it runs %s, which declares %s, and takes %s", c.DeclaredBy, FormatMB(c.MemoryMB), slots)
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
			desc += " (" + FormatMB(h.MemoryMB) + ")"
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
