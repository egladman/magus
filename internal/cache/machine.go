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

	// now is the reading a test cannot stage. Nil means the real clock.
	now func() time.Time
}

type machineEntry struct {
	claim   types.MachineClaim
	started time.Time
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
func (b *MachineBudget) Request(c types.MachineClaim) types.MachineVerdict {
	b.mu.Lock()
	defer b.mu.Unlock()

	if c.Slots < 1 {
		c.Slots = 1
	}
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
	if !b.fits(c, heldMB, heldSlots) && !b.freeSlot(c.Ancestors) {
		othersMB, othersSlots := b.heldByOthers(c)
		v.OwnRun = b.fits(c, othersMB, othersSlots)
		return v
	}

	v.Granted, v.ID = true, b.record(c)
	v.HeldMB, v.HeldSlots = heldMB+c.MemoryMB, heldSlots+c.Slots
	return v
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
	excused := b.excusedClaims(c.Ancestors)
	run := c.Run()
	return b.sum(func(id string, held types.MachineClaim) bool {
		return excused[id] ||
			(c.PID != 0 && held.PID == c.PID) ||
			(run != "" && held.Run() == run)
	})
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
	out := make([]types.MachineClaimant, 0, len(b.claims))
	for id, e := range b.claims {
		if excused[id] {
			continue
		}
		out = append(out, types.MachineClaimant{
			Project: e.claim.Project, Target: e.claim.Target, PID: e.claim.PID,
			MemoryMB: e.claim.MemoryMB, Slots: e.claim.Slots,
			Dir: e.claim.Dir, Command: e.claim.Command, Since: e.started,
		})
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
