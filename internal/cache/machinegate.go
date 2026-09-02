package cache

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/types"
)

// MachineAdmitter is the budget as a client reaches it: the daemon over the proc
// socket, or a MachineBudget directly when this process IS the daemon.
type MachineAdmitter interface {
	Request(ctx context.Context, waiter string, c MachineClaim) (MachineVerdict, error)
	Release(ctx context.Context, id string)
	Drop(ctx context.Context, waiter string)
}

const (
	// machinePollEvery is how often a queued step re-asks. The budget never blocks, so
	// the wait is the client's, which is the process that can print it and the one whose
	// death should retire the waiter.
	machinePollEvery = 200 * time.Millisecond

	// machineWaitHeartbeat matches the project lock's cadence: a queued run must keep
	// saying it is queued, or it reads as hung.
	machineWaitHeartbeat = 15 * time.Second
)

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
	seq    int
	mu     sync.Mutex
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
func (g *machineGate) acquire(ctx context.Context, c MachineClaim) (func(), error) {
	if g == nil || g.admit == nil {
		return func() {}, nil
	}
	waiter := g.waiterID(c)
	v, err := g.admit.Request(ctx, waiter, c)
	if err != nil {
		return g.admitOpen(ctx, err), nil
	}
	switch {
	case v.Granted:
		return g.releaser(v.ID), nil
	case !v.Fits:
		return nil, machineTooBigError(c, v)
	case g.noWait:
		g.admit.Drop(ctx, waiter)
		return nil, machineBusyError(c, v)
	}
	return g.wait(ctx, waiter, c, v)
}

// wait polls until the budget admits the step. The notice names the holders up front
// and repeats on a heartbeat, because a queued run with nothing on screen is
// indistinguishable from a hung one.
func (g *machineGate) wait(ctx context.Context, waiter string, c MachineClaim, first MachineVerdict) (func(), error) {
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
				return nil, machineTooBigError(c, v)
			}
		}
	}
}

// releaser hands the claim back on a fresh context: the step's own is cancelled by the
// time a failing run tears down, and a release that skipped would leave the machine
// paying for work that has stopped.
func (g *machineGate) releaser(id string) func() {
	return func() { g.admit.Release(context.Background(), id) }
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

// waiterID identifies this step across its polls. The pid keeps it unique across the
// machine and the counter across concurrent steps in one process.
func (g *machineGate) waiterID(c MachineClaim) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seq++
	return fmt.Sprintf("%d.%d", c.Pid, g.seq)
}

// ancestorInvocations is the invocations this one runs underneath, this one excluded.
//
// The pid check is what makes the tail trustworthy as ours: an invocation that appends
// nothing leaves its PARENT's ref last, and dropping that would put the parent back in
// the competing set.
func ancestorInvocations(ctx context.Context) []string {
	refs := types.InvocationAncestorsFromContext(ctx)
	if len(refs) > 0 && mintedHere(refs[len(refs)-1]) {
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
func machineWaitingMessage(c MachineClaim, v MachineVerdict) string {
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
func machineBusyError(c MachineClaim, v MachineVerdict) error {
	return machineRefusal{types.DiagnosticErrorf(types.MachineBudgetExhausted,
		"not starting %s %s: this machine's build budget is full and MAGUS_NO_WAIT is set; %s, and %s. %s",
		displayProject(c.Project), c.Target, describeMachineDeclaration(c),
		describeMachineRemaining(v), describeMachineHolders(v.Holders))}
}

// machineTooBigError is the refusal no wait can fix: the declaration does not fit in
// the whole budget, so an empty machine would refuse it too.
func machineTooBigError(c MachineClaim, v MachineVerdict) error {
	return machineRefusal{types.DiagnosticErrorf(types.MachineBudgetExhausted,
		"refusing to start %s %s: %s, which does not fit in this machine's whole build budget of %s across %d slots. Waiting would not help; correct the declaration if it is wrong, or run this on a bigger machine.",
		displayProject(c.Project), c.Target, describeMachineDeclaration(c),
		FormatMB(v.BudgetMB), v.BudgetSlots)}
}

// describeMachineDeclaration says where the figure came from. A composed target is
// held over a number a target in its chain wrote, and a reader sent to its own policy
// would find nothing to change there.
func describeMachineDeclaration(c MachineClaim) string {
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

func describeMachineRemaining(v MachineVerdict) string {
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
func describeMachineHolders(holders []MachineHolder) string {
	if len(holders) == 0 {
		return "nothing else holds a claim"
	}
	const show = 4
	parts := make([]string, 0, min(len(holders), show))
	for _, h := range holders[:min(len(holders), show)] {
		desc := fmt.Sprintf("pid %d %s %s", h.Pid, displayProject(h.Project), h.Target)
		if h.MemoryMB > 0 {
			desc += " (" + FormatMB(h.MemoryMB) + ")"
		}
		if h.Cwd != "" {
			desc += ", in " + h.Cwd
		}
		parts = append(parts, desc)
	}
	list := strings.Join(parts, "; ")
	if len(holders) > show {
		list = fmt.Sprintf("%s and %d more", list, len(holders)-show)
	}
	return "held by " + list
}
