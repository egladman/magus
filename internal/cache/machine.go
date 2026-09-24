package cache

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/types"
)

// MachineBudget is admission across every magus on the host: one budget of
// concurrency slots and declared memory, arbitrated in one place.
//
// The Limiter cannot answer this. It is per-process, so N worktrees each admit up to
// their own capacity and nothing sums them. Both read the same folded figure: the
// limiter throttles peers within a process, this decides whether the machine can seat
// the step at all.
//
// It lives in the user's broker, the one process every magus on the host can reach. The
// broker hangs each claim off the connection that took it and releases it when that
// connection closes, so the budget trusts what it is told and never polls a pid: a
// holder killed outright is released by the kernel closing its socket.
//
// DECLARED, not observed, so the same command on the same host reaches the same
// verdict whatever a browser is doing. Observed pressure stays advisory in sys/mem.
type MachineBudget struct {
	mu          sync.Mutex
	budgetMB    int
	budgetSlots int
	claims      map[string]*machineEntry
	seq         int64
	// queue is the line of claims waiting for capacity, in arrival order.
	queue []*machineWaiter

	// now is the reading a test cannot stage. Nil means the real clock.
	now func() time.Time
}

type machineEntry struct {
	claim   types.MachineClaim
	started time.Time
}

type machineWaiter struct {
	key    uint64
	claim  types.MachineClaim
	since  time.Time
	passed int
}

// MachineOrder names how the line is served, for status: oldest first, with bounded
// backfill.
const MachineOrder = "fifo-backfill"

// BackfillLimit is how many later claims may be seated past a waiter before it becomes
// a barrier that only its own run, and nested children of holders, may pass.
//
// Strict head-of-line order wastes the host whenever the oldest waiter is large: a
// 16 GiB step at the head of the line would hold back every one-slot lint while
// memory sat idle. Unbounded backfill starves that same step, since small claims keep
// fitting into every gap it is waiting to see widen. Bounding it keeps both: a waiter
// is passed at most this many times, then capacity drains toward it, because every
// claim holding it belongs to a running step that ends. Small, because each pass can
// cost the waiter a whole step's duration.
const BackfillLimit = 4

// SeatedClaim is a waiter Seat granted; Key is what Enqueue was given.
type SeatedClaim struct {
	Key     uint64
	Verdict types.MachineVerdict
}

// QueuedClaim is a waiter still in line; Key is what Enqueue was given.
type QueuedClaim struct {
	Key  uint64
	Wait types.MachineWait
}

// NewMachineBudget returns a budget of budgetMB megabytes and budgetSlots concurrency
// slots. A non-positive figure leaves that half unlimited, the fallback for a host magus
// cannot measure.
func NewMachineBudget(budgetMB, budgetSlots int) *MachineBudget {
	return &MachineBudget{
		budgetMB:    budgetMB,
		budgetSlots: budgetSlots,
		claims:      map[string]*machineEntry{},
	}
}

func (b *MachineBudget) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// Request asks for admission once. It grants and records the claim, or answers why not
// and records nothing, and it never blocks: whether to ask again belongs to the client,
// which the verdict's OwnRun tells.
//
// A claim that fits may still be refused for the line: see BackfillLimit and
// MachineVerdict.Ahead.
func (b *MachineBudget) Request(c types.MachineClaim) types.MachineVerdict {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c.Slots < 1 {
		c.Slots = 1
	}
	return b.request(c, len(b.queue))
}

// Enqueue asks for admission and, when the claim would fit an empty host but not this
// one, puts it in line under key instead of refusing it. A granted verdict, or one that
// does not fit, is final; any other means the claim is waiting, until Seat grants it or
// Leave takes it out.
//
// Holding nothing while waiting is what keeps the line free of deadlock: a waiter asks
// for a step's whole need at once and holds none of it until all of it is granted, and
// every claim it waits behind belongs to a step that is running and will end. The one
// holder that cannot end on its own, a parent blocked in exec on a nested magus, is
// answered by freeSlot and by letting its children past the line.
func (b *MachineBudget) Enqueue(key uint64, c types.MachineClaim) types.MachineVerdict {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c.Slots < 1 {
		c.Slots = 1
	}
	v := b.request(c, len(b.queue))
	if !v.Granted && v.Fits {
		b.queue = append(b.queue, &machineWaiter{key: key, claim: c, since: b.clock()})
	}
	return v
}

// Leave takes the waiter under key out of line and reports whether it was still there.
// False means Seat already granted it, or it never waited.
func (b *MachineBudget) Leave(key uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, w := range b.queue {
		if w.key == key {
			b.queue = slices.Delete(b.queue, i, i+1)
			return true
		}
	}
	return false
}

// Seat grants every waiter that can be seated now, oldest first, and returns them. The
// caller runs it after anything that frees capacity or shortens the line.
//
// One pass suffices: a grant only takes capacity, so a waiter this pass skipped still
// cannot be seated once a later one is.
func (b *MachineBudget) Seat() []SeatedClaim {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []SeatedClaim
	for i := 0; i < len(b.queue); {
		w := b.queue[i]
		v := b.request(w.claim, i)
		if !v.Granted {
			i++
			continue
		}
		b.queue = slices.Delete(b.queue, i, i+1)
		out = append(out, SeatedClaim{Key: w.key, Verdict: v})
	}
	return out
}

// Waiting reports the line, oldest first.
func (b *MachineBudget) Waiting() []QueuedClaim {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]QueuedClaim, 0, len(b.queue))
	for i, w := range b.queue {
		othersMB, othersSlots := b.heldByOthers(w.claim)
		ahead := b.barriers(w.claim, i)
		out = append(out, QueuedClaim{Key: w.key, Wait: types.MachineWait{
			Claim:    w.claim,
			Position: i + 1,
			Since:    w.since,
			OwnRun:   len(ahead) == 0 && b.fits(w.claim, othersMB, othersSlots),
			BlockedBy: b.claimants(func(_ string, held types.MachineClaim) bool {
				return b.ownOrExcused(w.claim, held)
			}),
			Ahead:      ahead,
			PassedOver: w.passed,
		}})
	}
	return out
}

// request decides c as though it stood at position pos in line, and grants and records
// it when it may be seated. Callers hold b.mu.
func (b *MachineBudget) request(c types.MachineClaim, pos int) types.MachineVerdict {
	heldMB, heldSlots := b.held(c.Ancestors)
	v := types.MachineVerdict{
		Fits:        b.fits(c, 0, 0),
		Holders:     b.holders(c.Ancestors),
		BudgetMB:    b.budgetMB,
		HeldMB:      heldMB,
		BudgetSlots: b.budgetSlots,
		HeldSlots:   heldSlots,
	}
	if !v.Fits {
		return v
	}
	// A nested magus under a holder is how that holder finishes, so the line never
	// holds it back: a waiter ahead of it would be waiting on it.
	nested := b.nestedUnderHolder(c)
	if !nested {
		if v.Ahead = b.barriers(c, pos); len(v.Ahead) > 0 {
			return v
		}
	}
	if !b.fits(c, heldMB, heldSlots) && !b.freeSlot(c.Ancestors) {
		othersMB, othersSlots := b.heldByOthers(c)
		v.OwnRun = b.fits(c, othersMB, othersSlots)
		return v
	}
	if !nested {
		for _, w := range b.queue[:pos] {
			if !sameMachineRun(w.claim, c) {
				w.passed++
			}
		}
	}
	v.Granted, v.ID = true, b.record(c)
	v.HeldMB, v.HeldSlots = heldMB+c.MemoryMB, heldSlots+c.Slots
	return v
}

// barriers are the waiters ahead of position pos, from invocations other than c's,
// already passed over BackfillLimit times. Callers hold b.mu.
func (b *MachineBudget) barriers(c types.MachineClaim, pos int) []types.MachineClaimant {
	var out []types.MachineClaimant
	for _, w := range b.queue[:pos] {
		if w.passed >= BackfillLimit && !sameMachineRun(w.claim, c) {
			out = append(out, claimant(w.claim, w.since))
		}
	}
	return out
}

// nestedUnderHolder reports a claim one of whose ancestor invocations holds a claim: a
// magus started by a running step. Callers hold b.mu.
func (b *MachineBudget) nestedUnderHolder(c types.MachineClaim) bool {
	for _, e := range b.claims {
		if isMachineAncestor(e.claim.Invocation, c.Ancestors) {
			return true
		}
	}
	return false
}

// sameMachineRun reports two claims from one invocation tree or one process, which the
// line never orders against each other.
func sameMachineRun(a, b types.MachineClaim) bool {
	return (a.Run() != "" && a.Run() == b.Run()) || (a.PID != 0 && a.PID == b.PID)
}

// Assert records a claim without asking whether it fits, and returns its id. It is for
// a claim granted by a broker that has since died: the step is already running, so its
// memory is spent whatever this budget thinks, and refusing to record it would admit
// new work against memory that is in use.
func (b *MachineBudget) Assert(c types.MachineClaim) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c.Slots < 1 {
		c.Slots = 1
	}
	return b.record(c)
}

// record stores c under a fresh id. Callers hold b.mu.
func (b *MachineBudget) record(c types.MachineClaim) string {
	b.seq++
	id := fmt.Sprintf("%d.%d", c.PID, b.seq)
	b.claims[id] = &machineEntry{claim: c, started: b.clock()}
	return id
}

// Release returns a granted claim. An unknown id is ignored: a client that lost its
// broker mid-run releases into a budget that never recorded it.
func (b *MachineBudget) Release(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.claims, id)
}

// Snapshot reports the whole budget. Read-only, so a status command can ask what the
// machine is doing without changing it.
func (b *MachineBudget) Snapshot() types.MachineSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	heldMB, heldSlots := b.held(nil)
	return types.MachineSnapshot{
		BudgetMB:    b.budgetMB,
		HeldMB:      heldMB,
		BudgetSlots: b.budgetSlots,
		HeldSlots:   heldSlots,
		Holders:     b.holders(nil),
	}
}

// held sums what counts against a claim with these ancestors: every live claim except
// the ones its ancestor invocations hold.
//
// The ancestor exclusion is what makes a nested magus possible. A target whose suite
// runs `magus run test .` is a descendant of a run already holding that target's
// declaration, and counting it would refuse its own child. The memory is not doubled
// either, since the ancestor is blocked in exec.
//
// EVERY claim an ancestor invocation holds, not just the largest. A parent running four
// steps at once holds four claims, and a child excused from only one waits behind three
// that nobody can return until this child has run: under-excluding hangs, where
// over-excluding only blinds a child to its parent's concurrent siblings. Sibling
// DESCENDANTS still count against each other, so a fan-out stays bounded, and a
// declaration larger than the whole budget is still refused.
//
// Two INDEPENDENT roots whose children each need more than the other root leaves free
// are past what any exclusion here can reach, since a holder does not report whether it
// is blocked. freeSlot answers that pair structurally.
func (b *MachineBudget) held(ancestors []string) (mb, slots int) {
	excused := b.excusedClaims(ancestors)
	return b.sum(func(id string, _ types.MachineClaim) bool { return excused[id] })
}

// heldByOthers is held less the claims of c's own process and of c's own run: the part
// of a full budget that nothing c's run is doing will release. When c fits beside that
// alone, a refusal would fail the run over a wait on itself.
//
// The same process covers the server's adopted runs and a run's own concurrent steps;
// the same run covers sibling nested magus processes under one root.
func (b *MachineBudget) heldByOthers(c types.MachineClaim) (mb, slots int) {
	return b.sum(func(_ string, held types.MachineClaim) bool { return b.ownOrExcused(c, held) })
}

// ownOrExcused reports a held claim that is not another invocation's as far as c is
// concerned: one an ancestor of c holds, or one from c's own process or run.
func (b *MachineBudget) ownOrExcused(c, held types.MachineClaim) bool {
	return isMachineAncestor(held.Invocation, c.Ancestors) ||
		(c.PID != 0 && held.PID == c.PID) ||
		(c.Run() != "" && held.Run() == c.Run())
}

// sum totals every claim skip does not exclude.
func (b *MachineBudget) sum(skip func(id string, c types.MachineClaim) bool) (mb, slots int) {
	for id, e := range b.claims {
		if skip(id, e.claim) {
			continue
		}
		mb += e.claim.MemoryMB
		slots += e.claim.Slots
	}
	return mb, slots
}

// excusedClaims is every claim an ancestor invocation holds; none of them counts
// against a descendant, by the reasoning on held.
func (b *MachineBudget) excusedClaims(ancestors []string) map[string]bool {
	if len(ancestors) == 0 {
		return nil
	}
	out := map[string]bool{}
	for id, e := range b.claims {
		if isMachineAncestor(e.claim.Invocation, ancestors) {
			out[id] = true
		}
	}
	return out
}

// freeSlot reports whether a claim that does not otherwise fit may be seated anyway.
//
// It is GNU Make's jobserver rule: every make instance may run one job without holding
// a token, so the bottom of every chain can always run and recursive make cannot wedge
// however it nests. The entitlement belongs to the blocked ANCESTOR, and only while
// nothing under that ancestor is running.
//
// Both halves of that are load-bearing. Make's tokens are unit-sized, so seating one
// free job per REQUESTER over-admits by a bounded amount; a claim here is megabytes, and
// one parent fanning four cross-process children of 4000 MB each would free-seat all
// four on a 12000 MB machine. Charging the seat to the stalled ancestor caps
// over-admission at one claim per stalled ancestor, which is make's own bound.
//
// The running-descendant test keeps a fan-out bounded rather than a stampede: a child
// kept out by its own siblings is waiting for steps that are running, not for steps
// waiting on it, so it needs no seat and waits for them (see heldByOthers). The pair
// this DOES answer is two independent roots blocked in exec on children that each need
// more than the other root leaves free. Each child has an ancestor holding a claim with
// nothing running beneath it, so each is seated and both roots finish.
//
// Nothing is stored. A released claim stops counting as its ancestor's running
// descendant, so the seat comes back through the same release as any other claim. A claim with no ancestor holding anything gets no seat, so a stranger cannot
// get past a full budget with this, and a declaration larger than the whole budget is
// refused before Request reaches here.
func (b *MachineBudget) freeSlot(ancestors []string) bool {
	if len(ancestors) == 0 {
		return false
	}
	holding, running := map[string]bool{}, map[string]bool{}
	for _, e := range b.claims {
		if e.claim.Invocation != "" {
			holding[e.claim.Invocation] = true
		}
		for _, a := range e.claim.Ancestors {
			running[a] = true
		}
	}
	for _, a := range ancestors {
		if holding[a] && !running[a] {
			return true
		}
	}
	return false
}

// fits reports whether c still fits once mb and slots are spoken for. A non-positive
// budget is unlimited on that axis.
func (b *MachineBudget) fits(c types.MachineClaim, mb, slots int) bool {
	if b.budgetMB > 0 && mb+c.MemoryMB > b.budgetMB {
		return false
	}
	return b.budgetSlots <= 0 || slots+c.Slots <= b.budgetSlots
}

// holders lists every claim that counts against a claim with these ancestors, oldest
// first, so a refusal names who is holding the budget.
func (b *MachineBudget) holders(ancestors []string) []types.MachineClaimant {
	excused := b.excusedClaims(ancestors)
	return b.claimants(func(id string, _ types.MachineClaim) bool { return excused[id] })
}

// claimants lists every claim skip does not exclude, oldest first.
func (b *MachineBudget) claimants(skip func(id string, c types.MachineClaim) bool) []types.MachineClaimant {
	out := make([]types.MachineClaimant, 0, len(b.claims))
	for id, e := range b.claims {
		if skip(id, e.claim) {
			continue
		}
		out = append(out, claimant(e.claim, e.started))
	}
	slices.SortFunc(out, func(a, b types.MachineClaimant) int {
		if !a.Since.Equal(b.Since) {
			return a.Since.Compare(b.Since)
		}
		if a.Project != b.Project {
			return strings.Compare(a.Project, b.Project)
		}
		return strings.Compare(a.Target, b.Target)
	})
	return out
}

func claimant(c types.MachineClaim, since time.Time) types.MachineClaimant {
	return types.MachineClaimant{
		Project: c.Project, Target: c.Target, PID: c.PID,
		MemoryMB: c.MemoryMB, Slots: c.Slots,
		Dir: c.Dir, Command: c.Command, Since: since,
	}
}

func isMachineAncestor(invocation string, ancestors []string) bool {
	return invocation != "" && slices.Contains(ancestors, invocation)
}

// FormatMB renders a declared memory figure. Base-1024 with binary suffixes and a
// space, matching FormatBytes rather than inventing a second spelling of the same
// quantity in one binary. Exported so a refusal and `magus status` say the same figure
// the same way.
func FormatMB(mb int) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GiB", float64(mb)/1024)
	}
	return fmt.Sprintf("%d MiB", mb)
}
