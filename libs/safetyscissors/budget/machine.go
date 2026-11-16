package budget

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Machine is admission across every process on the host: one budget of concurrency
// slots and declared memory, arbitrated in one place.
//
// The [Limiter] cannot answer this. It is per-process, so N checkouts each admit up to
// their own capacity and nothing sums them. Both read the same folded figure: the
// limiter throttles peers within a process, this decides whether the machine can seat
// the step at all.
//
// It belongs in one process every client on the host can reach. That arbiter hangs each
// claim off the connection that took it and releases it when that connection closes, so
// the budget trusts what it is told and never polls a pid: a holder killed outright is
// released by the kernel closing its socket.
//
// DECLARED, not observed, so the same command on the same host reaches the same verdict
// whatever a browser is doing. Refusing a machine the kernel calls failing is the
// caller's, before it asks. A claim with a memory curve is charged by how long it has
// run, a wall-clock fact rather than a host reading: two identical states at the same
// elapsed time agree.
//
// A Machine is safe for concurrent use.
type Machine struct {
	mu          sync.Mutex
	budgetMB    int
	budgetSlots int
	reserve     Reserve
	wrapper     func(invocation string) bool
	claims      map[string]*entry
	seq         int64

	// now is the clock curves are charged by. Nil means the real clock.
	now func() time.Time
}

type entry struct {
	claim   Claim
	started time.Time
	// overrun is set once the claim's own tree held more than its curve predicted.
	overrun bool
}

// MachineOption configures a [Machine] at construction.
type MachineOption func(*Machine)

// NewMachine returns a budget of budgetMB megabytes and budgetSlots concurrency
// slots. A non-positive figure leaves that half unlimited, the fallback for a host the
// caller cannot measure.
func NewMachine(budgetMB, budgetSlots int, opts ...MachineOption) *Machine {
	m := &Machine{
		budgetMB:    budgetMB,
		budgetSlots: budgetSlots,
		claims:      map[string]*entry{},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// WithReserve records what the memory budget left the OS, for Snapshot to name.
func WithReserve(r Reserve) MachineOption {
	return func(m *Machine) { m.reserve = r }
}

// WithWrappers names the invocations that only wrap a command: they hold slots and run
// no step, so nothing is blocked in exec above their children. Without it every
// invocation is a step, and a wrapper's child may be seated free on its account.
func WithWrappers(isWrapper func(invocation string) bool) MachineOption {
	return func(m *Machine) { m.wrapper = isWrapper }
}

// WithClock charges curved claims by now instead of the real clock, so a caller that
// replays or simulates a host reaches the verdict the host would have.
func WithClock(now func() time.Time) MachineOption {
	return func(m *Machine) { m.now = now }
}

func (b *Machine) isWrapper(invocation string) bool {
	return b.wrapper != nil && b.wrapper(invocation)
}

func (b *Machine) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// Request asks for admission once. It grants and records the claim, or answers why not
// and records nothing, and it never blocks: whether to ask again belongs to the client,
// which the verdict's OwnRun tells.
//
// Memory is admitted by curve: c is seated when at every future instant it fits beside
// what counts against it (see fitsOverTime), so two claims whose peaks would not fit
// together are seated when their curves say the peaks fall apart. Slots stay peaks.
//
// c is seated at SlotsFloor(c) and granted more up to Slots.
// queued is the other claims the arbiter holds parked. With
// none a grant takes what is free; with some, a fair share.
// Slots carries the grant. A verdict that does not grant
// names queued in Ahead.
func (b *Machine) Request(c Claim, queued int) Verdict {
	b.mu.Lock()
	defer b.mu.Unlock()

	if c.Slots < 1 {
		c.Slots = 1
	}
	seat := c
	seat.Slots = SlotsFloor(c)
	held := b.held(seat)
	heldMB, heldSlots := b.sum(held)
	v := Verdict{
		Fits:        b.fitsOverTime(seat, func(string, Claim) bool { return true }),
		Packed:      b.packs(seat, held),
		Holders:     b.holders(seat),
		BudgetMB:    b.budgetMB,
		HeldMB:      heldMB,
		BudgetSlots: b.budgetSlots,
		HeldSlots:   heldSlots,
	}
	if !v.Fits {
		return v
	}
	if !b.fitsOverTime(seat, held) && !b.freeSlot(seat.Ancestors) {
		v.OwnRun = b.fitsOverTime(seat, b.heldByOthers(seat))
		v.Ahead = queued
		return v
	}

	seat.Slots = b.grant(c.Slots, seat.Slots, heldSlots, queued)
	v.Granted, v.ID, v.Slots = true, b.record(seat), seat.Slots
	v.HeldMB, v.HeldSlots = heldMB+c.MemoryMB, heldSlots+seat.Slots
	return v
}

// SlotsFloor is the fewest slots c runs with. An unset or
// oversized SlotsFloor means Slots: a claim of fixed width.
func SlotsFloor(c Claim) int {
	ceiling := max(c.Slots, 1)
	if c.SlotsFloor < 1 || c.SlotsFloor > ceiling {
		return ceiling
	}
	return c.SlotsFloor
}

// grant is the slots a seated claim takes beside heldSlots.
// Each waiter keeps a share of what is free, so a burst
// starts narrow. One seated past a full budget takes its floor.
func (b *Machine) grant(ceiling, floor, heldSlots, queued int) int {
	if b.budgetSlots <= 0 {
		return ceiling
	}
	share := b.budgetSlots - heldSlots
	if queued > 0 {
		share = max(1, share/(queued+1))
	}
	return max(floor, min(ceiling, share))
}

// Adopt records a claim without asking whether it fits, and returns its id. It is for
// a claim granted by an arbiter that has since died: the step is already running, so its
// memory is spent whatever this budget thinks, and refusing to record it would admit
// new work against memory that is in use.
func (b *Machine) Adopt(c Claim) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c.Slots < 1 {
		c.Slots = 1
	}
	return b.record(c)
}

// record stores c under a fresh id. Callers hold b.mu.
func (b *Machine) record(c Claim) string {
	b.seq++
	id := fmt.Sprintf("%d.%d", c.PID, b.seq)
	b.claims[id] = &entry{claim: c, started: b.clock()}
	return id
}

// Release returns a granted claim. An unknown id is ignored: a client that lost its
// arbiter mid-run releases into a budget that never recorded it.
func (b *Machine) Release(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.claims, id)
}

// Overrun charges a granted claim its full peak, MemoryMB, until it is released: its
// run held more than its curve predicted, so the curve no longer bounds it. Nothing is
// preempted. An unknown id is ignored, as Release ignores one.
func (b *Machine) Overrun(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e, ok := b.claims[id]; ok {
		e.overrun = true
	}
}

// Snapshot reports the whole budget. Read-only, so a status command can ask what the
// machine is doing without changing it.
func (b *Machine) Snapshot() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	heldMB, heldSlots := b.sum(b.held(Claim{}))
	return Snapshot{
		BudgetMB:    b.budgetMB,
		HeldMB:      heldMB,
		BudgetSlots: b.budgetSlots,
		HeldSlots:   heldSlots,
		Holders:     b.holders(Claim{}),
		Reserve:     b.reserve,
	}
}

// held sums what counts against c: every live claim except the ones excused names.
//
// The ancestor exclusion is what makes a nested run possible. A target whose suite
// starts another run of the build tool is a descendant of a run already holding that target's
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
//
// It returns the skip that sum and fitsOverTime take.
func (b *Machine) held(c Claim) func(id string, c Claim) bool {
	excused := b.excused(c)
	return func(id string, _ Claim) bool { return excused[id] }
}

// heldByOthers is held less the claims of c's own process and of c's own run: the part
// of a full budget that nothing c's run is doing will release. When c fits beside that
// alone, a refusal would fail the run over a wait on itself.
//
// The same process covers the server's adopted runs and a run's own concurrent steps;
// the same run covers sibling nested processes under one root.
func (b *Machine) heldByOthers(c Claim) func(id string, c Claim) bool {
	excused := b.excused(c)
	run := c.Run()
	return func(id string, held Claim) bool {
		return excused[id] ||
			(c.PID != 0 && held.PID == c.PID) ||
			(run != "" && held.Run() == run)
	}
}

// fitsOverTime reports whether c, starting now, fits beside every claim skip does not
// exclude at every future instant (see breach). A non-positive budget is unlimited on
// that axis.
func (b *Machine) fitsOverTime(c Claim, skip func(id string, c Claim) bool) bool {
	_, breached := b.breach(c, skip)
	return !breached
}

// breach is the first instant c, starting now, and the claims skip does not exclude
// together pass the budget. Each contribution is a step function (see heldAt), so
// checking now and every claim's breakpoints s + k*D10/K and s + k*D90/K is exact.
// Slots are peaks and breach now.
func (b *Machine) breach(c Claim, skip func(id string, c Claim) bool) (time.Time, bool) {
	now := b.clock()
	_, slots := b.sum(skip)
	if b.budgetSlots > 0 && slots+max(c.Slots, 1) > b.budgetSlots {
		return now, true
	}
	if b.budgetMB <= 0 {
		return time.Time{}, false
	}
	counted := []entry{{claim: c, started: now}}
	for id, e := range b.claims {
		if !skip(id, e.claim) {
			counted = append(counted, *e)
		}
	}
	instants := []time.Time{now}
	for _, e := range counted {
		if curved(e, now) {
			breakpoints(e, func(t time.Time) {
				if t.After(now) {
					instants = append(instants, t)
				}
			})
		}
	}
	var first time.Time
	breached := false
	for _, t := range instants {
		total := 0
		for _, e := range counted {
			total += heldAt(e, now, t)
		}
		if total > b.budgetMB && (!breached || t.Before(first)) {
			first, breached = t, true
		}
	}
	return first, breached
}

// breakpoints calls yield with each instant e's curve can step at, s + k*D10/K and
// s + k*D90/K, rounded up.
func breakpoints(e entry, yield func(time.Time)) {
	for _, ms := range []int64{e.claim.Profile.D10Ms, e.claim.Profile.D90Ms} {
		d := int64(time.Duration(ms) * time.Millisecond)
		for k := range int64(CurveBuckets + 1) {
			yield(e.started.Add(time.Duration((k*d + CurveBuckets - 1) / CurveBuckets)))
		}
	}
}

// NextChange is the next instant a curved claim crosses a breakpoint, so
// the budget changes with no claim released. ok is false when nothing will.
// An arbiter sets its wake timer to it for the requests it holds.
func (b *Machine) NextChange() (at time.Time, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock()
	consider := func(t time.Time) {
		if t.After(now) && (!ok || t.Before(at)) {
			at, ok = t, true
		}
	}
	for _, e := range b.claims {
		if curved(*e, now) {
			breakpoints(*e, consider)
		}
	}
	return at, ok
}

// curved reports whether e is charged by its curve at now: it has a profile with a
// duration band, has not yet run past its D90 and has not overrun its curve.
func curved(e entry, now time.Time) bool {
	p := e.claim.Profile
	d90 := time.Duration(p.D90Ms) * time.Millisecond
	return !e.overrun && p.Runs > 0 && d90 > 0 && now.Sub(e.started) <= d90
}

// heldAt is what e holds at t, judged at now. A claim that is not curved (see curved)
// holds its flat MemoryMB until released. A curved claim is
// expected released past its D90 and holds nothing there; a run that is still held
// then is an overrun, which the next verdict charges its peak.
func heldAt(e entry, now, t time.Time) int {
	if !curved(e, now) {
		return e.claim.MemoryMB
	}
	elapsed := t.Sub(e.started)
	if elapsed > time.Duration(e.claim.Profile.D90Ms)*time.Millisecond {
		return 0
	}
	return min(e.claim.Profile.HeldMB(elapsed), e.claim.MemoryMB)
}

// packs reports whether a verdict on c beside the claims skip does not exclude judges
// any of them by curve.
func (b *Machine) packs(c Claim, skip func(id string, c Claim) bool) bool {
	now := b.clock()
	if curved(entry{claim: c, started: now}, now) {
		return true
	}
	for id, e := range b.claims {
		if !skip(id, e.claim) && curved(*e, now) {
			return true
		}
	}
	return false
}

// sum totals every claim skip does not exclude.
func (b *Machine) sum(skip func(id string, c Claim) bool) (mb, slots int) {
	for id, e := range b.claims {
		if skip(id, e.claim) {
			continue
		}
		mb += e.claim.MemoryMB
		slots += e.claim.Slots
	}
	return mb, slots
}

// excused is every claim that does not count against c: each one an ancestor invocation
// holds, by the reasoning on held.
func (b *Machine) excused(c Claim) map[string]bool {
	out := map[string]bool{}
	for id, e := range b.claims {
		if isMachineAncestor(e.claim.Invocation, c.Ancestors) {
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
// descendant, so the seat comes back through the same release as any other claim. A
// claim with no ancestor holding anything gets no seat, so a stranger cannot get past a full budget with this, and a declaration larger than the whole budget is
// refused before Request reaches here.
func (b *Machine) freeSlot(ancestors []string) bool {
	if len(ancestors) == 0 {
		return false
	}
	holding, running := map[string]bool{}, map[string]bool{}
	for _, e := range b.claims {
		// A wrapper runs no step: nothing is blocked in exec above its child,
		// and it spends no seat of a step above it.
		if b.isWrapper(e.claim.Invocation) {
			continue
		}
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

// holders lists every claim that counts against c, oldest first, so a refusal names who
// is holding the budget.
func (b *Machine) holders(c Claim) []Claimant {
	excused := b.excused(c)
	out := make([]Claimant, 0, len(b.claims))
	for id, e := range b.claims {
		if excused[id] {
			continue
		}
		out = append(out, Claimant{
			Project: e.claim.Project, Target: e.claim.Target, PID: e.claim.PID,
			MemoryMB: e.claim.MemoryMB, Profile: e.claim.Profile, Sizing: e.claim.Sizing, Slots: e.claim.Slots,
			Dir: e.claim.Dir, Command: e.claim.Command, Since: e.started, Under: e.claim.Under,
		})
	}
	slices.SortFunc(out, func(a, b Claimant) int {
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

// Breach is the first instant c, starting at now, and holders, each started at its Since,
// together pass budgetMB, judging memory by curve as [Machine.Request] does; ok is false
// when they never do. It explains a verdict after the fact from the holders the verdict
// named.
func Breach(budgetMB int, holders []Claimant, c Claim, now time.Time) (at time.Time, ok bool) {
	b := NewMachine(budgetMB, 0, WithClock(func() time.Time { return now }))
	for i, h := range holders {
		b.claims[strconv.Itoa(i)] = &entry{
			claim:   Claim{MemoryMB: h.MemoryMB, Profile: h.Profile},
			started: h.Since,
		}
	}
	return b.breach(c, func(string, Claim) bool { return false })
}
