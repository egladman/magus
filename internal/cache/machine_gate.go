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

	runPkg "github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/types"
)

// MachineAdmitter is the budget as a client reaches it: the broker, over the one
// connection this process holds to it.
type MachineAdmitter interface {
	Request(ctx context.Context, c types.MachineClaim) (types.MachineVerdict, error)
	Release(ctx context.Context, id string)
}

// machineReleaseTimeout bounds handing a claim back. The release runs in the defer
// that still holds this step's local limiter slot, so it must never outlast a sick
// broker.
//
// A var, not a const, for the same reason as elsewhere in this package: a timing a test
// cannot shorten is a timing that either costs real wall-clock or goes uncovered.
var machineReleaseTimeout = 5 * time.Second

// machinePollEvery paces a step kept out only by claims its own process or run holds.
// The budget has no wakeup to push across the socket, so the step asks again.
const machinePollEvery = 100 * time.Millisecond

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
// waits on its own run, or refuses.
type machineGate struct {
	admit MachineAdmitter
	log   *slog.Logger
	lost  sync.Once
	// required refuses a step the admitter could not answer for, where the default
	// admits it unarbitrated.
	required bool
}

// acquire claims the machine budget for one step and returns the function that frees
// it, or an MGS3009 error naming who holds it.
//
// A step declaring nothing still takes a slot: concurrency is the half every step
// spends. A step kept out only by claims of its own process or its own run waits for
// them, until ctx ends; one kept out by any other magus invocation is refused at once
// (exit 75), since magus never waits on another magus invocation.
//
// A broker that dies, or a transport that breaks, admits the step and says so once
// under best-effort; under required it refuses with MGS3022 (exit 69).
func (g *machineGate) acquire(ctx context.Context, c types.MachineClaim) (func(), error) {
	if g == nil || g.admit == nil {
		return func() {}, nil
	}
	for {
		v, err := g.admit.Request(ctx, c)
		if err != nil {
			if g.required {
				return nil, brokerUnavailableError(c, err)
			}
			return g.admitOpen(ctx, err), nil
		}
		switch {
		case v.Granted:
			return g.releaser(v.ID), nil
		case !v.Fits:
			return nil, machineDoesNotFitError(c, v)
		case v.OwnRun:
			// Falls out of the switch to the wait below.
		case blindToOwnAncestry(ctx):
			// A nested magus that cannot name its ancestors cannot be excused from its own
			// parent's claim, so the refusal names the fix rather than the holders.
			return nil, machineBlindError(c, v)
		default:
			return nil, machineBusyError(c, v)
		}
		// The holders are this run's own steps, whose progress feeds the stall watchdog,
		// or another invocation of this process, whose progress does not; beat for the
		// latter, or a run queued in the server trips MGS3012.
		ProgressFromContext(ctx).Beat()
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("machine budget: gave up waiting for %s %s: %w", displayProject(c.Project), c.Target, ctx.Err())
		case <-time.After(machinePollEvery):
		}
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

// machineBusyError is the fail-fast answer: another magus invocation fills the machine
// right now, and the same command will succeed later.
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

// describeMachineOversize says what to change, which is not the same sentence for the two
// axes. A memory figure is a declaration magus has already measured against, so the reader
// is sent to that check rather than to a guess; a slot count is bounded by the cores and
// has nothing to check.
//
// This no longer names a fixed percentage: mem.BudgetMB reserves a share of memory that
// depends on the profile the daemon started under (a quarter, under balanced or
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
