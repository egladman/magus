package cache

import (
	"fmt"
	"slices"
	"strconv"
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
// It lives in the user's broker, the one process every magus on the host can reach. The
// broker hangs each claim off the connection that took it and releases it when that
// connection closes, so the budget trusts what it is told and never polls a pid: a
// holder killed outright is released by the kernel closing its socket.
//
// DECLARED, not observed, so the same command on the same host reaches the same
// verdict whatever a browser is doing. Observed pressure stays advisory in sys/mem. A
// claim with a memory curve is charged by how long it has run, a wall-clock fact rather
// than a host reading: two identical states at the same elapsed time agree.
type MachineBudget struct {
	mu          sync.Mutex
	budgetMB    int
	budgetSlots int
	claims      map[string]*machineEntry
	seq         int64

	// now is the reading a test cannot stage. Nil means the real clock.
	now func() time.Time
	// alive reports whether a reservation's reader still runs. Nil means pid.Alive.
	alive func(pid int) bool
}

type machineEntry struct {
	claim   types.MachineClaim
	started time.Time
	// reader is the process a reservation is held for; 0 for a granted claim.
	reader int
	// overrun is set once the claim's own tree held more than its curve predicted.
	overrun bool
}

// reservationBound is how long a reservation waits for its reader's first claim. It
// covers the reader loading its workspace and taking its locks; a reader still running
// past it, or one whose every step replays, gives the seat back to the budget.
const reservationBound = 30 * time.Second

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
// Memory is admitted by curve: c is seated when at every future instant it fits beside
// what counts against it (see fitsOverTime), so two claims whose peaks would not fit
// together are seated when their curves say the peaks fall apart. Slots stay peaks.
//
// A seat reserved for c's process (see Reserve) does not count against c, and granting c
// consumes it.
func (b *MachineBudget) Request(c types.MachineClaim) types.MachineVerdict {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked()

	if c.Slots < 1 {
		c.Slots = 1
	}
	held := b.held(c)
	heldMB, heldSlots := b.sum(held)
	v := types.MachineVerdict{
		Fits:        b.fitsOverTime(c, func(string, types.MachineClaim) bool { return true }),
		Packed:      b.packs(c, held),
		Holders:     b.holders(c),
		BudgetMB:    b.budgetMB,
		HeldMB:      heldMB,
		BudgetSlots: b.budgetSlots,
		HeldSlots:   heldSlots,
	}
	if !v.Fits {
		return v
	}
	if !b.fitsOverTime(c, held) && !b.freeSlot(c.Ancestors) {
		v.OwnRun = b.fitsOverTime(c, b.heldByOthers(c))
		return v
	}

	for id, e := range b.claims {
		if e.reserves(c) {
			delete(b.claims, id)
		}
	}
	v.Granted, v.ID = true, b.record(c)
	v.HeldMB, v.HeldSlots = heldMB+c.MemoryMB, heldSlots+c.Slots
	return v
}

// Reserve holds a seat the size of c for process reader, when the budget can seat c now
// beside everything it holds. The reader's first granted claim consumes the seat; until
// then it counts against every other claim. It lapses once the reader is gone or has
// claimed nothing for reservationBound, whichever comes first, so it never outlives its
// reader. A second reservation for one reader replaces the first.
//
// Like Request it never blocks: a verdict that is not Granted reserved nothing, and Fits
// says whether asking again can help. reader must be positive.
func (b *MachineBudget) Reserve(c types.MachineClaim, reader int) types.MachineVerdict {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked()
	for id, e := range b.claims {
		if e.reader == reader {
			delete(b.claims, id)
		}
	}
	c.Slots, c.PID = max(c.Slots, 1), reader
	heldMB, heldSlots := b.sum(b.held(types.MachineClaim{}))
	v := types.MachineVerdict{
		Fits:        b.fits(c, 0, 0),
		Holders:     b.holders(types.MachineClaim{}),
		BudgetMB:    b.budgetMB,
		HeldMB:      heldMB,
		BudgetSlots: b.budgetSlots,
		HeldSlots:   heldSlots,
	}
	if !v.Fits || !b.fits(c, heldMB, heldSlots) {
		return v
	}
	v.Granted, v.ID = true, b.record(c)
	b.claims[v.ID].reader = reader
	v.HeldMB, v.HeldSlots = heldMB+c.MemoryMB, heldSlots+c.Slots
	return v
}

// DropReservations gives back every reserved seat, for a broker that seats nothing new.
func (b *MachineBudget) DropReservations() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, e := range b.claims {
		if e.reader != 0 {
			delete(b.claims, id)
		}
	}
}

// pruneLocked drops each reservation whose reader has exited or whose bound has passed.
// It runs before every reading of the budget, so a lapsed seat is never counted. Callers
// hold b.mu.
func (b *MachineBudget) pruneLocked() {
	now := b.clock()
	alive := b.alive
	if alive == nil {
		alive = pid.Alive
	}
	for id, e := range b.claims {
		if e.reader != 0 && (now.Sub(e.started) >= reservationBound || !alive(e.reader)) {
			delete(b.claims, id)
		}
	}
}

// reserves reports whether e is a seat reserved for the process taking c: c's own, or
// the outermost invocation c runs under, which is the reader when the claim comes from a
// nested magus or from the daemon adopting the reader's run.
func (e *machineEntry) reserves(c types.MachineClaim) bool {
	if e.reader == 0 {
		return false
	}
	if c.PID == e.reader {
		return true
	}
	owner, _, ok := strings.Cut(c.Run(), ":")
	return ok && owner == strconv.Itoa(e.reader)
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

// Overrun charges a granted claim its full peak, MemoryMB, until it is released: its
// run held more than its curve predicted, so the curve no longer bounds it. Nothing is
// preempted. An unknown id is ignored, as Release ignores one.
func (b *MachineBudget) Overrun(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e, ok := b.claims[id]; ok {
		e.overrun = true
	}
}

// Snapshot reports the whole budget. Read-only, so a status command can ask what the
// machine is doing without changing it.
func (b *MachineBudget) Snapshot() types.MachineSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked()
	heldMB, heldSlots := b.sum(b.held(types.MachineClaim{}))
	return types.MachineSnapshot{
		BudgetMB:    b.budgetMB,
		HeldMB:      heldMB,
		BudgetSlots: b.budgetSlots,
		HeldSlots:   heldSlots,
		Holders:     b.holders(types.MachineClaim{}),
	}
}

// held sums what counts against c: every live claim and reservation except the ones
// excused names.
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
//
// It returns the skip that sum and fitsOverTime take.
func (b *MachineBudget) held(c types.MachineClaim) func(id string, c types.MachineClaim) bool {
	excused := b.excused(c)
	return func(id string, _ types.MachineClaim) bool { return excused[id] }
}

// heldByOthers is held less the claims of c's own process and of c's own run: the part
// of a full budget that nothing c's run is doing will release. When c fits beside that
// alone, a refusal would fail the run over a wait on itself.
//
// The same process covers the server's adopted runs and a run's own concurrent steps;
// the same run covers sibling nested magus processes under one root.
func (b *MachineBudget) heldByOthers(c types.MachineClaim) func(id string, c types.MachineClaim) bool {
	excused := b.excused(c)
	run := c.Run()
	return func(id string, held types.MachineClaim) bool {
		return excused[id] ||
			(c.PID != 0 && held.PID == c.PID) ||
			(run != "" && held.Run() == run)
	}
}

// fitsOverTime reports whether c, starting now, fits beside every claim skip does not
// exclude at every future instant (see breach). A non-positive budget is unlimited on
// that axis.
func (b *MachineBudget) fitsOverTime(c types.MachineClaim, skip func(id string, c types.MachineClaim) bool) bool {
	_, breached := b.breach(c, skip)
	return !breached
}

// breach is the first instant c, starting now, and the claims skip does not exclude
// together pass the budget. Each contribution is a step function (see heldAt), so
// checking now and every claim's breakpoints s + k*D10/K and s + k*D90/K is exact.
// Slots are peaks and breach now.
func (b *MachineBudget) breach(c types.MachineClaim, skip func(id string, c types.MachineClaim) bool) (time.Time, bool) {
	now := b.clock()
	_, slots := b.sum(skip)
	if b.budgetSlots > 0 && slots+max(c.Slots, 1) > b.budgetSlots {
		return now, true
	}
	if b.budgetMB <= 0 {
		return time.Time{}, false
	}
	counted := []machineEntry{{claim: c, started: now}}
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
func breakpoints(e machineEntry, yield func(time.Time)) {
	for _, ms := range []int64{e.claim.Profile.D10Ms, e.claim.Profile.D90Ms} {
		d := int64(time.Duration(ms) * time.Millisecond)
		for k := range int64(types.MemoryCurveBuckets + 1) {
			yield(e.started.Add(time.Duration((k*d + types.MemoryCurveBuckets - 1) / types.MemoryCurveBuckets)))
		}
	}
}

// NextChange is the next instant the budget changes with no claim released: a
// reservation reaching its bound, or a curved claim crossing a breakpoint. ok is false
// when nothing will. A broker sets its wake timer to it for the requests it holds.
func (b *MachineBudget) NextChange() (at time.Time, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock()
	consider := func(t time.Time) {
		if t.After(now) && (!ok || t.Before(at)) {
			at, ok = t, true
		}
	}
	for _, e := range b.claims {
		if e.reader != 0 {
			consider(e.started.Add(reservationBound))
		}
		if curved(*e, now) {
			breakpoints(*e, consider)
		}
	}
	return at, ok
}

// curved reports whether e is charged by its curve at now: it has a profile with a
// duration band, has not yet run past its D90 and has not overrun its curve.
func curved(e machineEntry, now time.Time) bool {
	p := e.claim.Profile
	d90 := time.Duration(p.D90Ms) * time.Millisecond
	return !e.overrun && p.Runs > 0 && d90 > 0 && now.Sub(e.started) <= d90
}

// heldAt is what e holds at t, judged at now. A claim that is not curved (see curved)
// holds its flat MemoryMB until released. A curved claim is
// expected released past its D90 and holds nothing there; a run that is still held
// then is an overrun, which the next verdict charges its peak.
func heldAt(e machineEntry, now, t time.Time) int {
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
func (b *MachineBudget) packs(c types.MachineClaim, skip func(id string, c types.MachineClaim) bool) bool {
	now := b.clock()
	if curved(machineEntry{claim: c, started: now}, now) {
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

// excused is every claim that does not count against c: each one an ancestor invocation
// holds, by the reasoning on held, and the seat reserved for c's process.
func (b *MachineBudget) excused(c types.MachineClaim) map[string]bool {
	out := map[string]bool{}
	for id, e := range b.claims {
		if isMachineAncestor(e.claim.Invocation, c.Ancestors) || e.reserves(c) {
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

// holders lists every claim that counts against c, oldest first, so a refusal names who
// is holding the budget.
func (b *MachineBudget) holders(c types.MachineClaim) []types.MachineClaimant {
	excused := b.excused(c)
	out := make([]types.MachineClaimant, 0, len(b.claims))
	for id, e := range b.claims {
		if excused[id] {
			continue
		}
		out = append(out, types.MachineClaimant{
			Reserved: e.reader != 0, Project: e.claim.Project, Target: e.claim.Target, PID: e.claim.PID,
			MemoryMB: e.claim.MemoryMB, Profile: e.claim.Profile, Sizing: e.claim.Sizing, Slots: e.claim.Slots,
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
