package cache

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	runPkg "github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/sys/mem"
	"github.com/egladman/magus/types"
)

// MachineAdmitter is the budget as a client reaches it: the daemon over the proc
// socket, or a MachineBudget directly when this process IS the daemon.
type MachineAdmitter interface {
	Request(ctx context.Context, waiter string, c types.MachineClaim) (types.MachineVerdict, error)
	Release(ctx context.Context, id string)
	Drop(ctx context.Context, waiter string)
}

// machineReleaseTimeout bounds handing a claim back. The release runs in the defer
// that still holds this step's local limiter slot, so it must never outlast a sick
// daemon.
//
// A var, not a const, for the same reason as elsewhere in this package: a timing a test
// cannot shorten is a timing that either costs real wall-clock or goes uncovered.
var machineReleaseTimeout = 5 * time.Second

// machineWaiterSeq numbers waiters within this PROCESS, not within a Cache.
//
// The daemon holds one Cache per workspace and every one of them reports the daemon's
// pid, so a per-Cache counter handed two workspaces the same "<pid>.1" and their
// waiters overwrote each other in the registry: one run's queue entry silently became
// the other's, and the budget then reserved for a claim nobody was waiting on.
var machineWaiterSeq atomic.Int64

// ExitCodeMachineBusy is the process status a machine-budget refusal asks for: 75,
// EX_TEMPFAIL. It lives here rather than beside the CLI's other exit codes because the
// error is built here and has to state its own code: the daemon runs an adopted step
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

// machineGate is the client half of admission: it asks the budget once and either
// proceeds or refuses; it never queues behind another magus process.
type machineGate struct {
	admit MachineAdmitter
	log   *slog.Logger
	lost  sync.Once
}

// acquire claims the machine budget for one step and returns the function that frees
// it, or an MGS3009 error naming who holds it.
//
// A step declaring nothing still takes a slot: concurrency is the half every step
// spends. magus never waits on another magus process, so a step that does not fit right
// now is refused immediately (exit 75) rather than queued.
//
// Fails OPEN. A daemon that dies, or a transport that breaks, admits the step and says
// so once: losing the arbiter must not stop a build that was going to run.
func (g *machineGate) acquire(ctx context.Context, c types.MachineClaim) (func(), error) {
	if g == nil || g.admit == nil {
		return func() {}, nil
	}
	waiter := machineWaiterID(c)
	v, err := g.admit.Request(ctx, waiter, c)
	if err != nil {
		return g.admitOpen(ctx, err), nil
	}
	switch {
	case v.Granted:
		return g.releaser(v.ID), nil
	case !v.Fits:
		return nil, machineDoesNotFitError(c, v)
	case blindToOwnAncestry(ctx):
		// A nested magus that cannot name its ancestors cannot be excused from its own
		// parent's claim, so queueing here is queueing behind a step that is blocked in
		// exec waiting for THIS process: a permanent deadlock. Refusing turns it into an
		// answer a caller can act on.
		g.admit.Drop(ctx, waiter)
		return nil, machineBlindError(c, v)
	default:
		g.admit.Drop(ctx, waiter)
		return nil, machineBusyError(c, v)
	}
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
// against a wedged daemon would pin that slot for as long as the daemon stays wedged,
// turning one sick process into a stalled run.
func (g *machineGate) releaser(id string) func() {
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), machineReleaseTimeout)
		defer cancel()
		g.admit.Release(ctx, id)
	}
}

// admitOpen is the fail-open path: the arbiter is unreachable, so this run proceeds
// unarbitrated. Said once, because a run whose every step repeats it teaches the
// reader to scroll past the line.
func (g *machineGate) admitOpen(ctx context.Context, err error) func() {
	g.lost.Do(func() {
		g.log.WarnContext(ctx, "magus: machine-wide admission is OFF for this run: the daemon holding the budget is unreachable",
			slog.String("error", err.Error()))
	})
	return func() {}
}

// machineWaiterID identifies this step across its polls. The pid keeps it unique across
// the machine and the process-wide counter across every concurrent step in this one,
// whichever Cache they belong to.
func machineWaiterID(c types.MachineClaim) string {
	return fmt.Sprintf("%d.%d", c.PID, machineWaiterSeq.Add(1))
}

// ancestorInvocations is the invocations this one runs underneath, this one excluded.
//
// The pid check is what makes the tail trustworthy as ours: an invocation that appends
// nothing leaves its PARENT's ref last, and dropping that would put the parent back in
// the competing set.
//
// The CLI and the daemon stamp ancestry onto ctx at their own entry points; a LIBRARY
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

// The exit status is per refusal (types.ExitError, and see proc.ExitCode for the daemon
// side). EX_TEMPFAIL says "try again"; a declaration that cannot fit and a nested magus
// that lost its ancestry both answer the same way forever, so a wrapper retrying on 75
// would loop on them.

// machineBusyError is the fail-fast answer: the machine is full right now, and the same
// command will succeed later. magus never queues behind another magus process, so this
// is the only answer a full budget ever gets.
func machineBusyError(c types.MachineClaim, v types.MachineVerdict) error {
	return types.ExitError{Code: ExitCodeMachineBusy, Err: types.DiagnosticErrorf(types.MachineBudgetExhausted,
		"not starting %s %s: this machine's build budget is full; %s, and %s. %s",
		displayProject(c.Project), c.Target, describeMachineDeclaration(c),
		describeMachineRemaining(v), describeMachineHolders(v.Holders))}
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

// machineBudgetPercent is mem.UsableFraction as a whole number, so a refusal can name the
// share instead of restating it as prose that drifts the day the constant moves. A reader
// who does not know about the fraction reads the budget as a miscount of their own RAM.
var machineBudgetPercent = int(mem.UsableFraction * 100)

// describeMachineOversize says what to change, which is not the same sentence for the two
// axes. A memory figure is a declaration magus has already measured against, so the reader
// is sent to that check rather than to a guess; a slot count is bounded by the cores and
// has nothing to check.
func describeMachineOversize(c types.MachineClaim, v types.MachineVerdict) string {
	if v.BudgetMB > 0 && c.MemoryMB > v.BudgetMB {
		return fmt.Sprintf("that budget is %d%% of the memory available here; the rest runs the OS and everything else. Run `magus doctor` and read MGS1030, which compares this declaration to the peak memory magus measured: correct the declaration if it has drifted; if it is honest, get a bigger machine.",
			machineBudgetPercent)
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
