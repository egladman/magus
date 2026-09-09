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

// machineDeadlockGrace is how long a waiter must see progress stay impossible before the
// budget calls it a deadlock. A single poll is a photograph: a holder can be one
// instruction from releasing when it is sampled, and refusing on that reading turns a
// won race into a build failure. A var rather than a const for the reason machinegate
// states about its own timings: a test that has to spend the real cadence either sleeps
// for it or does not cover it.
//
// Well under machineWaiterStaleAfter, so the verdict lands while the waiters that prove
// it are still registered.
var machineDeadlockGrace = 3 * time.Second

type machineEntry struct {
	claim    types.MachineClaim
	started  time.Time
	lastSeen time.Time
	// blockedSince is when this WAITER first saw progress become impossible, cleared the
	// moment it becomes possible again. Only a condition that persists is a deadlock; see
	// wedged.
	blockedSince time.Time
}

// NewMachineBudget returns a budget of budgetMB megabytes and budgetSlots concurrency
// slots. A non-positive figure leaves that half unlimited, which is what a host magus
// cannot measure falls back to.
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
	if !b.fits(c, heldMB+reserveMB, heldSlots+reserveSlots) {
		v.Ahead = b.ahead(waiter)
		// Some waits cannot end, and this is the only place that can tell: say so rather
		// than let the client heartbeat at a queue that will never move. The waiter stays
		// registered either way, because leaving the queue is the CLIENT's move here as
		// it is for every other refusal.
		if stuck := b.stuckHolders(c.Ancestors); b.wedged(w, c, stuck, now) {
			v.Deadlocked, v.Stuck = true, b.claimants(stuck, nil)
		}
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

// Snapshot reports the whole budget. Read-only: it retires nothing, so a status
// command can ask what the machine is doing without moving a queue. Entries whose
// process is gone are FILTERED rather than deleted, so the report never shows a corpse
// the next Request would retire anyway.
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
// EVERY claim an ancestor invocation holds, not just the largest. An invocation is not
// a step: a parent running four steps at once holds four claims, and a child excused
// from only one queues behind three that nobody can return until this child has run.
// Excusing all of them does blind a child to its parent's genuinely concurrent
// siblings, but the alternative is not throttling, it is a hang; under-excluding is the
// direction that deadlocks. Sibling DESCENDANTS still count against each other, so a
// fan-out stays bounded, and a declaration larger than the whole budget is still
// refused.
//
// Two INDEPENDENT roots whose children each need more than the other root leaves free
// still wedge each other, and no exclusion rule closes that: each root's claim is a
// stranger's to the other's child, and excusing a stranger is the throttling this
// exists to do. Request DETECTS that case instead and refuses it; see wedged.
func (b *MachineBudget) held(ancestors []string) (mb, slots int) {
	excused := b.excusedClaims(ancestors)
	for id, e := range b.claims {
		if excused[id] {
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

// stuckHolders is every counted holder that cannot release: it is an ancestor of some
// REGISTERED WAITER, so it is blocked in exec on a step this budget has queued and not
// seated. A holder whose descendants are all running is not stuck, and neither is one
// with no descendant asking at all.
//
// Claims this waiter is excused from are skipped, by the reasoning on held: an ancestor
// of ours holds nothing we compete for, and seating us is what unblocks it.
func (b *MachineBudget) stuckHolders(ancestors []string) map[string]*machineEntry {
	excused := b.excusedClaims(ancestors)
	out := map[string]*machineEntry{}
	for id, e := range b.claims {
		if excused[id] || e.claim.Invocation == "" {
			continue
		}
		for _, w := range b.waiters {
			if isMachineAncestor(e.claim.Invocation, w.claim.Ancestors) {
				out[id] = e
				break
			}
		}
	}
	return out
}

// wedged reports the wait that cannot end: releasing every holder that CAN still finish
// would not seat this claim, because what is left belongs to runs blocked on queued
// descendants. Waiting is then a hang the heartbeat reports as healthy forever.
//
// It asks whether the STUCK memory alone already excludes this claim, which is what
// keeps it quiet in the two shapes a naive wait-for cycle misreads:
//
//   - Two independent roots, 12 GB budget, each holding 6 GB while blocked on a 7 GB
//     child. Our own root is excused, the other is stuck, and 6+7 exceeds 12, so no order
//     of releases seats us: it fires, which is the deadlock this exists for.
//   - The same pair holding 4 GB each. 4+7 fits, so the child is granted outright and
//     never reaches here; queued behind a reservation it still fits, and the answer stays
//     keep waiting, which is right because the peer root does finish.
//   - One parent fanning out four steps, each shelling out to a magus of its own. The
//     three running children are ancestors of NOBODY waiting, so the stuck sum is zero
//     and the fourth keeps its place in the queue: correct, since a running sibling ends
//     and frees it.
//
// The grace period is what separates a wedge from a holder that was one instruction from
// releasing when it happened to be sampled. Keeping it is why this takes the waiter's
// entry rather than the two figures: the timer belongs to the run that is waiting.
func (b *MachineBudget) wedged(w *machineEntry, c types.MachineClaim, stuck map[string]*machineEntry, now time.Time) bool {
	mb, slots := 0, 0
	for _, e := range stuck {
		mb += e.claim.MemoryMB
		slots += e.claim.Slots
	}
	if b.fits(c, mb, slots) {
		w.blockedSince = time.Time{}
		return false
	}
	if w.blockedSince.IsZero() {
		w.blockedSince = now
	}
	return now.Sub(w.blockedSince) >= machineDeadlockGrace
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
// so the figure can exceed the true queue depth by the size of the tie. It is a
// progress indicator in a sentence, not a position anything schedules on, and rounding
// it up reads as the more honest error.
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
