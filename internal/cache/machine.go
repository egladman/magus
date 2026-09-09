package cache

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/sys/pid"
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
// It lives in the user's daemon, which is what makes a queue possible. An earlier
// attempt kept it in a flocked lease directory and had to refuse rather than wait,
// because a passive directory cannot tell a waiter when its turn came. A process can.
//
// DECLARED, not observed, so the same command on the same host reaches the same
// verdict whatever a browser is doing. Observed pressure stays advisory in sys/mem.
type MachineBudget struct {
	mu          sync.Mutex
	budgetMB    int
	budgetSlots int
	claims      map[string]*machineEntry
	waiters     map[string]*machineEntry
	seq         int64

	// now and alive are the two readings a test cannot stage. Nil means the real one.
	now   func() time.Time
	alive func(int) bool
}

const (
	// machineClaimStaleAfter bounds how long a claim is believed. Liveness alone cannot
	// retire one: a daemon that outlives a reboot holds a pid the kernel has since handed
	// to something else, and pid.Alive says yes forever. Losing a long run's claim is the
	// safer failure.
	machineClaimStaleAfter = 24 * time.Hour

	// machineWaiterStaleAfter drops a waiter that stopped asking. Every poll refreshes
	// one, so this only fires for a client that died mid-wait; without it a corpse at the
	// head of the queue reserves memory nobody wants.
	machineWaiterStaleAfter = 30 * time.Second
)

type machineEntry struct {
	claim    types.MachineClaim
	started  time.Time
	lastSeen time.Time
}

// NewMachineBudget returns a budget of budgetMB megabytes and budgetSlots concurrency
// slots. A non-positive figure leaves that half unlimited, the fallback for a host magus
// cannot measure.
func NewMachineBudget(budgetMB, budgetSlots int) *MachineBudget {
	return &MachineBudget{
		budgetMB:    budgetMB,
		budgetSlots: budgetSlots,
		claims:      map[string]*machineEntry{},
		waiters:     map[string]*machineEntry{},
	}
}

func (b *MachineBudget) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

func (b *MachineBudget) live(p int) bool {
	if b.alive != nil {
		return b.alive(p)
	}
	return pid.Alive(p)
}

// Request is one poll for admission on behalf of waiter, an id stable across this
// step's polls and unique machine-wide. It grants, refuses, or queues, and never
// blocks: the wait belongs to the client, which is the process that can report it.
func (b *MachineBudget) Request(waiter string, c types.MachineClaim) types.MachineVerdict {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reap()

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
		delete(b.waiters, waiter)
		return v
	}

	now := b.clock()
	w, queued := b.waiters[waiter]
	if !queued {
		w = &machineEntry{claim: c, started: now}
		b.waiters[waiter] = w
	}
	w.claim, w.lastSeen = c, now

	// Reserve for the head of the queue. Granting whatever fits right now is simpler
	// and starves the claim this exists for: a ci gate reserving 10GB would sit behind
	// an unbroken stream of one-slot runs forever.
	reserveMB, reserveSlots := b.headReservation(waiter, c.Ancestors)
	if !b.fits(c, heldMB+reserveMB, heldSlots+reserveSlots) && !b.freeSlot(c.Ancestors) {
		v.Ahead = b.ahead(waiter)
		return v
	}

	delete(b.waiters, waiter)
	b.seq++
	id := fmt.Sprintf("%d.%d", c.PID, b.seq)
	b.claims[id] = &machineEntry{claim: c, started: now, lastSeen: now}
	v.Granted, v.ID = true, id
	v.HeldMB, v.HeldSlots = heldMB+c.MemoryMB, heldSlots+c.Slots
	return v
}

// Release returns a granted claim. An unknown id is ignored: a client that lost its
// daemon mid-run releases into a budget that never recorded it.
func (b *MachineBudget) Release(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.claims, id)
}

// Drop retires a waiter that gave up, so the queue behind it moves now rather than
// after machineWaiterStaleAfter.
func (b *MachineBudget) Drop(waiter string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.waiters, waiter)
}

// Snapshot reports the whole budget. Read-only: it retires nothing, so a status command
// can ask what the machine is doing without moving a queue. Entries whose process is
// gone are FILTERED out of the lists AND out of the totals rather than deleted, so the
// report never shows a corpse the next Request would retire anyway.
//
// held carries the same liveness skip as claimants for this caller alone; Request cannot
// tell the difference, since reap has already run by the time it asks. Filtering only
// the lists left the two halves answering different questions, and the arithmetic is
// what the ci gate reads as saturation, so a hard-killed run refused every gate on an
// idle machine.
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
		Waiters:     b.claimants(b.waiters, nil),
	}
}

// reap drops what the budget must stop believing: a claim whose process is gone or
// that is older than any honest build, and a waiter that stopped asking.
func (b *MachineBudget) reap() {
	now := b.clock()
	for id, e := range b.claims {
		if now.Sub(e.started) > machineClaimStaleAfter || !b.live(e.claim.PID) {
			delete(b.claims, id)
		}
	}
	for id, e := range b.waiters {
		if now.Sub(e.lastSeen) > machineWaiterStaleAfter || !b.live(e.claim.PID) {
			delete(b.waiters, id)
		}
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
// steps at once holds four claims, and a child excused from only one queues behind three
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
	for id, e := range b.claims {
		if excused[id] || !b.live(e.claim.PID) {
			continue
		}
		mb += e.claim.MemoryMB
		slots += e.claim.Slots
	}
	return mb, slots
}

// excusedClaims is every claim an ancestor invocation holds; none of them counts
// against a descendant, by the reasoning on held. It shares isMachineAncestor with
// headReservation so the granted and the queued halves of the exclusion cannot drift.
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
// The running-descendant test keeps a fan-out a queue rather than a stampede: a child
// queued behind its own siblings is waiting for steps that are running, not for steps
// waiting on it, so it needs no seat. The pair this DOES answer is two independent roots
// blocked in exec on children that each need more than the other root leaves free. Each
// child has an ancestor holding a claim with nothing running beneath it, so each is
// seated and both roots finish.
//
// Nothing is stored. A released or reaped claim stops counting as its ancestor's running
// descendant, so the seat comes back through the same pid-liveness path as any other
// claim. A claim with no ancestor holding anything gets no seat, so a stranger cannot
// jump the queue with this, and a declaration larger than the whole budget is refused
// before Request reaches here.
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

// headReservation is what the oldest waiting claim needs, so a younger one cannot take
// the room it is queued for. Zero when this waiter IS the head.
//
// A waiter belonging to one of this claim's ANCESTORS is skipped, and the next-oldest
// stranger reserved instead. Reserving for an ancestor deadlocks the pair outright: the
// parent step is blocked in exec waiting for this child, so it cannot reach the front
// of the queue until the child runs, and the child will not run while it reserves room
// for the parent. held() already excuses an ancestor's granted claim for the same
// reason; a queued one has to be excused on the same terms or the exclusion has a hole
// exactly the width of the deadlock.
func (b *MachineBudget) headReservation(waiter string, ancestors []string) (mb, slots int) {
	head := ""
	var headAt time.Time
	for id, e := range b.waiters {
		if id == waiter || isMachineAncestor(e.claim.Invocation, ancestors) {
			continue
		}
		if head == "" || e.started.Before(headAt) || (e.started.Equal(headAt) && id < head) {
			head, headAt = id, e.started
		}
	}
	if head == "" {
		return 0, 0
	}
	// Only a waiter OLDER than this one reserves against it; a younger stranger is
	// behind us in the queue and reserving for it would invert the order.
	me, queued := b.waiters[waiter]
	if queued && !headAt.Before(me.started) {
		return 0, 0
	}
	c := b.waiters[head].claim
	return c.MemoryMB, c.Slots
}

// ahead counts the waiters queued in front of this one, so a wait notice can say where
// it stands.
//
// Ties count as ahead: two waiters minted in the same clock tick each report the other,
// so the figure can exceed the true queue depth by the size of the tie. It feeds a wait
// notice and nothing schedules on it, so rounding up is the honest error.
func (b *MachineBudget) ahead(waiter string) int {
	me, ok := b.waiters[waiter]
	if !ok {
		return 0
	}
	n := 0
	for id, e := range b.waiters {
		if id != waiter && !e.started.After(me.started) {
			n++
		}
	}
	return n
}

func (b *MachineBudget) holders(ancestors []string) []types.MachineClaimant {
	return b.claimants(b.claims, b.excusedClaims(ancestors))
}

// claimants projects an entry set onto the wire type, dropping any whose process has
// gone. Sorted oldest first, so a reader meets the queue in the order it will move.
func (b *MachineBudget) claimants(entries map[string]*machineEntry, excused map[string]bool) []types.MachineClaimant {
	out := make([]types.MachineClaimant, 0, len(entries))
	for id, e := range entries {
		if excused[id] || !b.live(e.claim.PID) {
			continue
		}
		out = append(out, types.MachineClaimant{
			Project: e.claim.Project, Target: e.claim.Target, PID: e.claim.PID,
			MemoryMB: e.claim.MemoryMB, Slots: e.claim.Slots,
			Dir: e.claim.Dir, Since: e.started,
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

// LocalAdmitter reaches a budget held in THIS process. It is what the daemon's own
// workspaces use: dialing its own socket from inside a request it is serving would
// have it wait on itself.
type LocalAdmitter struct{ Budget *MachineBudget }

// errNoLocalBudget is what a LocalAdmitter with no budget answers. An error rather than
// a grant: the gate reads it as "no arbiter" and fails open with a notice, where a
// silent grant would report an arbitrated run that was never arbitrated.
var errNoLocalBudget = errors.New("cache: this process holds no machine budget")

func (l LocalAdmitter) Request(_ context.Context, waiter string, c types.MachineClaim) (types.MachineVerdict, error) {
	if l.Budget == nil {
		return types.MachineVerdict{}, errNoLocalBudget
	}
	return l.Budget.Request(waiter, c), nil
}

func (l LocalAdmitter) Release(_ context.Context, id string) {
	if l.Budget != nil {
		l.Budget.Release(id)
	}
}

func (l LocalAdmitter) Drop(_ context.Context, waiter string) {
	if l.Budget != nil {
		l.Budget.Drop(waiter)
	}
}

// FormatMB renders a declared memory figure. Base-1024 with binary suffixes and a
// space, matching fmtBytesLog rather than inventing a second spelling of the same
// quantity in one binary. Exported so a refusal, a wait notice, and `magus status` all
// say the same figure the same way.
func FormatMB(mb int) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GiB", float64(mb)/1024)
	}
	return fmt.Sprintf("%d MiB", mb)
}
