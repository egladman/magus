package cache

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/sys/pid"
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

// MachineClaim is one step's request on the machine budget. It crosses the proc socket,
// so every field carries its wire name; numeric fields use omitzero because the jsonv2
// codec's omitempty does not omit 0.
type MachineClaim struct {
	Project    string `json:"project"`
	Target     string `json:"target"`
	DeclaredBy string `json:"declared_by,omitempty"` // absent when that is Target itself
	MemoryMB   int    `json:"memory_mb,omitzero"`
	Slots      int    `json:"slots,omitzero"`
	Pid        int    `json:"pid"`
	Cwd        string `json:"cwd,omitempty"`
	Command    string `json:"command,omitempty"`
	// Invocation is this run's own reference, recorded so a DESCENDANT magus can tell a
	// claim it runs underneath from one competing with it.
	Invocation string `json:"invocation,omitempty"`
	// Ancestors is the invocations this claim runs underneath, which do not count
	// against it.
	Ancestors []string `json:"ancestors,omitempty"`
}

// MachineHolder is a live claim as a waiter or `magus status` sees it.
type MachineHolder struct {
	Project  string    `json:"project"`
	Target   string    `json:"target"`
	Pid      int       `json:"pid"`
	MemoryMB int       `json:"memory_mb,omitzero"`
	Slots    int       `json:"slots,omitzero"`
	Cwd      string    `json:"cwd,omitempty"`
	Command  string    `json:"command,omitempty"`
	Started  time.Time `json:"started"`
}

// MachineVerdict is the budget's answer to one request.
type MachineVerdict struct {
	// Granted means the claim is recorded and ID releases it.
	Granted bool   `json:"granted"`
	ID      string `json:"id,omitempty"`
	// Fits is false when no state of the machine admits this claim, so waiting is a
	// hang rather than a queue.
	Fits        bool            `json:"fits"`
	Holders     []MachineHolder `json:"holders,omitempty"`
	Ahead       int             `json:"ahead,omitzero"` // waiters queued in front of this one
	BudgetMB    int             `json:"budget_mb,omitzero"`
	HeldMB      int             `json:"held_mb,omitzero"`
	BudgetSlots int             `json:"budget_slots,omitzero"`
	HeldSlots   int             `json:"held_slots,omitzero"`
}

// MachineSnapshot is the whole budget, for `magus status`.
type MachineSnapshot struct {
	BudgetMB    int             `json:"budget_mb,omitzero"`
	HeldMB      int             `json:"held_mb,omitzero"`
	BudgetSlots int             `json:"budget_slots,omitzero"`
	HeldSlots   int             `json:"held_slots,omitzero"`
	Holders     []MachineHolder `json:"holders,omitempty"`
	Waiters     []MachineHolder `json:"waiters,omitempty"`
}

type machineEntry struct {
	claim    MachineClaim
	started  time.Time
	lastSeen time.Time
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
// step's polls. It grants, refuses, or queues, and never blocks: the wait belongs to
// the client, which is the process that can report it.
func (b *MachineBudget) Request(waiter string, c MachineClaim) MachineVerdict {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reap()

	if c.Slots < 1 {
		c.Slots = 1
	}
	heldMB, heldSlots := b.held(c.Ancestors)
	v := MachineVerdict{
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
	reserveMB, reserveSlots := b.headReservation(waiter)
	if !b.fits(c, heldMB+reserveMB, heldSlots+reserveSlots) {
		v.Ahead = b.ahead(waiter)
		return v
	}

	delete(b.waiters, waiter)
	b.seq++
	id := fmt.Sprintf("%d.%d", c.Pid, b.seq)
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
// command can ask what the machine is doing without moving a queue.
func (b *MachineBudget) Snapshot() MachineSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	heldMB, heldSlots := b.held(nil)
	return MachineSnapshot{
		BudgetMB:    b.budgetMB,
		HeldMB:      heldMB,
		BudgetSlots: b.budgetSlots,
		HeldSlots:   heldSlots,
		Holders:     b.holders(nil),
		Waiters:     sortMachineHolders(b.waiters),
	}
}

// reap drops what the budget must stop believing: a claim whose process is gone or
// that is older than any honest build, and a waiter that stopped asking.
func (b *MachineBudget) reap() {
	now := b.clock()
	for id, e := range b.claims {
		if now.Sub(e.started) > machineClaimStaleAfter || !b.live(e.claim.Pid) {
			delete(b.claims, id)
		}
	}
	for id, e := range b.waiters {
		if now.Sub(e.lastSeen) > machineWaiterStaleAfter || !b.live(e.claim.Pid) {
			delete(b.waiters, id)
		}
	}
}

// held sums what counts against a claim with these ancestors: every live claim except
// the ones an invocation this claim runs UNDERNEATH is holding.
//
// The ancestor exclusion is what makes a nested magus possible. A target whose suite
// runs `magus run test .` is a descendant of a run already holding that target's
// declaration, and counting it would refuse its own child. The memory is not doubled
// either, since the ancestor is blocked in exec.
func (b *MachineBudget) held(ancestors []string) (mb, slots int) {
	for _, e := range b.claims {
		if isMachineAncestor(e.claim.Invocation, ancestors) {
			continue
		}
		mb += e.claim.MemoryMB
		slots += e.claim.Slots
	}
	return mb, slots
}

// fits reports whether c still fits once mb and slots are spoken for. A non-positive
// budget is unlimited on that axis.
func (b *MachineBudget) fits(c MachineClaim, mb, slots int) bool {
	if b.budgetMB > 0 && mb+c.MemoryMB > b.budgetMB {
		return false
	}
	return b.budgetSlots <= 0 || slots+c.Slots <= b.budgetSlots
}

// headReservation is what the oldest waiting claim needs, so a younger one cannot take
// the room it is queued for. Zero when this waiter IS the head.
func (b *MachineBudget) headReservation(waiter string) (mb, slots int) {
	head := ""
	var headAt time.Time
	for id, e := range b.waiters {
		if head == "" || e.started.Before(headAt) || (e.started.Equal(headAt) && id < head) {
			head, headAt = id, e.started
		}
	}
	if head == "" || head == waiter {
		return 0, 0
	}
	c := b.waiters[head].claim
	return c.MemoryMB, c.Slots
}

// ahead counts the waiters queued in front of this one, so a wait notice can say where
// it stands.
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

func (b *MachineBudget) holders(ancestors []string) []MachineHolder {
	out := make(map[string]*machineEntry, len(b.claims))
	for id, e := range b.claims {
		if isMachineAncestor(e.claim.Invocation, ancestors) {
			continue
		}
		out[id] = e
	}
	return sortMachineHolders(out)
}

func sortMachineHolders(m map[string]*machineEntry) []MachineHolder {
	out := make([]MachineHolder, 0, len(m))
	for _, e := range m {
		out = append(out, MachineHolder{
			Project: e.claim.Project, Target: e.claim.Target, Pid: e.claim.Pid,
			MemoryMB: e.claim.MemoryMB, Slots: e.claim.Slots,
			Cwd: e.claim.Cwd, Command: e.claim.Command, Started: e.started,
		})
	}
	slices.SortFunc(out, func(a, b MachineHolder) int {
		if !a.Started.Equal(b.Started) {
			return a.Started.Compare(b.Started)
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

func (l LocalAdmitter) Request(_ context.Context, waiter string, c MachineClaim) (MachineVerdict, error) {
	return l.Budget.Request(waiter, c), nil
}

func (l LocalAdmitter) Release(_ context.Context, id string) { l.Budget.Release(id) }

func (l LocalAdmitter) Drop(_ context.Context, waiter string) { l.Budget.Drop(waiter) }

// FormatMB renders megabytes as GB once MB stops being legible at a glance. Exported
// so a refusal, a wait notice, and `magus status` say the same figure the same way.
func FormatMB(mb int) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1fGB", float64(mb)/1024)
	}
	return fmt.Sprintf("%dMB", mb)
}
