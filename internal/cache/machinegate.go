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
	"github.com/egladman/magus/types"
)

// MachineAdmitter is the budget as a client reaches it: the daemon over the proc
// socket, or a MachineBudget directly when this process IS the daemon.
type MachineAdmitter interface {
	Request(ctx context.Context, waiter string, c types.MachineClaim) (types.MachineVerdict, error)
	Release(ctx context.Context, id string)
	Drop(ctx context.Context, waiter string)
}

// Vars, not consts, for the reason lock.go states about its own wait timings: a test
// that has to spend the real cadence either sleeps for it or does not cover it, and a
// wait whose reporting is untested is a wait that goes silent without anyone noticing.
var (
	// machinePollEvery is how often a queued step re-asks. The budget never blocks, so
	// the wait is the client's, which is the process that can print it and the one whose
	// death should retire the waiter.
	machinePollEvery = 200 * time.Millisecond

	// machineWaitHeartbeat matches the project lock's cadence: a queued run must keep
	// saying it is queued, or it reads as hung.
	machineWaitHeartbeat = 15 * time.Second

	// machineReleaseTimeout bounds handing a claim back. The release runs in the defer
	// that still holds this step's local limiter slot, so it must never outlast a sick
	// daemon.
	machineReleaseTimeout = 5 * time.Second
)

// machineWaiterSeq numbers waiters within this PROCESS, not within a Cache.
//
// The daemon holds one Cache per workspace and every one of them reports the daemon's
// pid, so a per-Cache counter handed two workspaces the same "<pid>.1" and their
// waiters overwrote each other in the registry: one run's queue entry silently became
// the other's, and the budget then reserved for a claim nobody was waiting on.
var machineWaiterSeq atomic.Int64

// ExitCodeMachineBusy is the process status a machine-budget refusal asks for: 75,
// EX_TEMPFAIL. It lives here rather than beside the CLI's other exit codes because the
// error is built here and has to state its own code - the daemon runs an adopted step
// in its own process and reads the code off the error, having lost the Go type.
//
// The workspace lock's own contention error picks the same number for the same reason
// (lockContendedExit). Two independent decisions that agree, not one shared setting:
// coupling them would make a change to either silently move the other.
const ExitCodeMachineBusy = 75

// machineGate is the client half of admission: it polls the budget, reports the wait,
// and hands back the release.
type machineGate struct {
	admit  MachineAdmitter
	noWait bool
	log    *slog.Logger
	lost   sync.Once
	// notify replaces the stderr writes when set, so a test observes the wait without
	// a terminal.
	notify func(string)
}

// acquire claims the machine budget for one step and returns the function that frees
// it, or an MGS3009 error naming who holds it.
//
// A step declaring nothing still takes a slot: concurrency is the half every step
// spends. Bounded by ctx; a wait ends when the caller gives up.
//
// Fails OPEN. A daemon that dies mid-wait, or a transport that breaks, admits the step
// and says so once: losing the arbiter must not stop a build that was going to run.
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
	case g.noWait:
		g.admit.Drop(ctx, waiter)
		return nil, machineBusyError(c, v)
	case blindToOwnAncestry(ctx):
		// A nested magus that cannot name its ancestors cannot be excused from its own
		// parent's claim, so queueing here is queueing behind a step that is blocked in
		// exec waiting for THIS process: a deadlock the heartbeat would report as "not
		// hung" forever. Refusing turns it into an answer a caller can act on.
		g.admit.Drop(ctx, waiter)
		return nil, machineBlindError(c, v)
	}
	return g.wait(ctx, waiter, c, v)
}

// blindToOwnAncestry reports a run that is inside a magus process tree and cannot say
// which invocations it is under. MAGUS_LEVEL says a magus started this one; an empty
// ancestry says we cannot tell which claims are our parent's.
//
// It asks ancestorInvocations rather than the context, so the two agree on what this run
// knows. Reading ctx alone made every library consumer blind by construction - the
// variable was in the process the whole time - and refused an in-process SDK run against
// its own parent's claim.
func blindToOwnAncestry(ctx context.Context) bool {
	return runPkg.CurrentLevel() > 0 && len(ancestorInvocations(ctx)) == 0
}

// wait polls until the budget admits the step. The notice names the holders up front
// and repeats on a heartbeat, because a queued run with nothing on screen is
// indistinguishable from a hung one.
func (g *machineGate) wait(ctx context.Context, waiter string, c types.MachineClaim, first types.MachineVerdict) (func(), error) {
	g.say(machineWaitingMessage(c, first))
	started := time.Now()
	poll := time.NewTicker(machinePollEvery)
	defer poll.Stop()
	beat := time.NewTicker(machineWaitHeartbeat)
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			g.admit.Drop(context.WithoutCancel(ctx), waiter)
			return nil, ctx.Err()
		case <-beat.C:
			g.say(fmt.Sprintf("magus: still queued for the machine budget (%s elapsed); this run is NOT hung. Set MAGUS_NO_WAIT=1 to fail fast instead.\n",
				time.Since(started).Round(time.Second)))
		case <-poll.C:
			v, err := g.admit.Request(ctx, waiter, c)
			if err != nil {
				// Leaving the queue on the way out matters most HERE: this waiter may be
				// the head, and the head's whole claim is reserved against every peer
				// until it is retired. A best-effort drop on a broken transport usually
				// fails, but when the daemon is merely slow rather than gone it saves
				// every other run on the machine a stale reservation.
				g.admit.Drop(context.WithoutCancel(ctx), waiter)
				return g.admitOpen(ctx, err), nil
			}
			switch {
			case v.Granted:
				g.say(fmt.Sprintf("magus: machine budget freed after %s; starting %s %s.\n",
					time.Since(started).Round(time.Second), displayProject(c.Project), c.Target))
				return g.releaser(v.ID), nil
			case !v.Fits:
				// A peer grew its claim while this one queued, and the budget can no
				// longer seat this step at all. No position in the queue fixes that.
				return nil, machineDoesNotFitError(c, v)
			}
		}
	}
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
// caller does not, and admission is the THIRD entry point to need this - the project
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

func (g *machineGate) say(msg string) {
	if g.notify != nil {
		g.notify(msg)
		return
	}
	fmt.Fprint(os.Stderr, msg)
}

// machineWaitingMessage is the one-shot notice a queued run prints. It names the
// holders because a wait a reader cannot attribute is a wait they can only interrupt.
func machineWaitingMessage(c types.MachineClaim, v types.MachineVerdict) string {
	ahead := ""
	if v.Ahead > 0 {
		ahead = fmt.Sprintf(", %d ahead of it", v.Ahead)
	}
	return fmt.Sprintf("magus: %s %s is queued for this machine's build budget%s; %s. This run starts automatically once room frees; set MAGUS_NO_WAIT=1 to fail fast instead.\n",
		displayProject(c.Project), c.Target, ahead, describeMachineHolders(v.Holders))
}

// machineRefusal is a refusal that states the process status it asks for. The
// diagnostic alone cannot: a step the daemon runs for an adopted client crosses a
// socket that erases the Go type, and the daemon reads the code off the error rather
// than naming a CLI package it must not import (see proc.ExitCode). Without this the
// same refusal exited 75 locally and 1 under a daemon.
//
// It wraps rather than replaces, so errors.Is against the diagnostic code keeps
// matching everywhere it already did.
type machineRefusal struct{ error }

func (machineRefusal) ExitCode() int { return ExitCodeMachineBusy }

func (e machineRefusal) Unwrap() error { return e.error }

// machineBusyError is the fail-fast answer: the machine is full right now, the same
// command will succeed later, and the caller asked not to queue.
func machineBusyError(c types.MachineClaim, v types.MachineVerdict) error {
	return machineRefusal{types.DiagnosticErrorf(types.MachineBudgetExhausted,
		"not starting %s %s: this machine's build budget is full and MAGUS_NO_WAIT is set; %s, and %s. %s",
		displayProject(c.Project), c.Target, describeMachineDeclaration(c),
		describeMachineRemaining(v), describeMachineHolders(v.Holders))}
}

// machineDoesNotFitError is the refusal no wait can fix: the declaration does not fit
// in the whole budget, so an empty machine would refuse it too.
func machineDoesNotFitError(c types.MachineClaim, v types.MachineVerdict) error {
	return machineRefusal{types.DiagnosticErrorf(types.MachineBudgetExhausted,
		"refusing to start %s %s: %s, which does not fit in this machine's whole build budget of %s across %d slots. Waiting would not help; correct the declaration if it is wrong, or run this on a bigger machine.",
		displayProject(c.Project), c.Target, describeMachineDeclaration(c),
		FormatMB(v.BudgetMB), v.BudgetSlots)}
}

// machineBlindError is the refusal for a nested magus that cannot name its ancestors.
// It says what to fix, because the cause is a magusfile clearing the environment rather
// than anything about the machine.
func machineBlindError(c types.MachineClaim, v types.MachineVerdict) error {
	return machineRefusal{types.DiagnosticErrorf(types.MachineBudgetExhausted,
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

// describeMachineHolders names who is holding the budget, so a wait or a refusal is a
// fact the reader can act on rather than a number.
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
