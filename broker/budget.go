package broker

import (
	"net"
	"slices"
	"time"

	"github.com/egladman/magus/types"
)

// refusedKeep bounds the refusals a broker remembers for status.
const refusedKeep = 20

// reservedKeep bounds the reserved seats whose owner the ledger remembers. A seat lapses
// within the budget's reservation bound, so the oldest past this many is long gone.
const reservedKeep = 64

// waitLapse is how long a waiter that stopped asking stays listed. A run asks again
// within a second and `status --wait` every half second, so one silent this long gave up
// without hanging up.
const waitLapse = 30 * time.Second

// waitKey is one caller's ask: the process and the step it waits to seat.
type waitKey struct {
	pid             int
	project, target string
	reservation     bool
}

type waitEntry struct {
	waiter types.MachineWaiter
	sess   *session
	asked  time.Time
}

type reservedSeat struct {
	owner string
	at    time.Time
}

// budgetLedger is what the broker remembers about its budget beyond the claims it holds:
// who waits for a seat now, who asked for each reserved seat, and the last MGS3009
// refusals it answered, each bounded. The zero value is ready; s.mu guards it.
type budgetLedger struct {
	waiting map[waitKey]*waitEntry
	seats   map[int]reservedSeat
	// refused is newest first.
	refused []types.MachineRefusal
}

// claimed records what a claim's verdict says about its caller: a grant ends its wait, a
// validation seat (seat non-nil) or its own run keeping it out is a wait, and anything
// else is the MGS3009 refusal the client turns it into.
func (l *budgetLedger) claimed(conns map[net.Conn]*session, sess *session, c types.MachineClaim, v types.MachineVerdict, seat *SeatWait, now time.Time) {
	key := waitKey{pid: c.PID, project: c.Project, target: c.Target}
	switch {
	case v.Granted:
		delete(l.waiting, key)
	case seat != nil:
		l.wait(key, sess, waiterOf(c, sessionOwner(sess), seat.Tree), now)
	case v.OwnRun:
		l.wait(key, sess, waiterOf(c, sessionOwner(sess), ""), now)
	default:
		delete(l.waiting, key)
		l.refuse(refusalOf(c, sessionOwner(sess), v, ownersOf(conns), now))
	}
}

// reserved records what a reservation's verdict says about the run it is asked for,
// reader: a grant is a seat the asker's owner holds, a claim that can never fit is a
// refusal, and anything else is a wait.
func (l *budgetLedger) reserved(conns map[net.Conn]*session, sess *session, c types.MachineClaim, reader int, v types.MachineVerdict, now time.Time) {
	c.PID = reader
	key := waitKey{pid: reader, project: c.Project, target: c.Target, reservation: true}
	owner := sessionOwner(sess)
	switch {
	case v.Granted:
		delete(l.waiting, key)
		l.reserve(reader, owner, now)
	case !v.Fits:
		delete(l.waiting, key)
		l.refuse(refusalOf(c, owner, v, ownersOf(conns), now))
	default:
		w := waiterOf(c, owner, "")
		w.Reservation = true
		l.wait(key, sess, w, now)
	}
}

// forget drops every wait sess was asking for: its connection closed.
func (l *budgetLedger) forget(sess *session) {
	for k, e := range l.waiting {
		if e.sess == sess {
			delete(l.waiting, k)
		}
	}
}

// capacity is snap with each holder's owner, every caller waiting now, and the refusals
// remembered. A reserved holder's owner is the one who asked for the seat; any other's
// is the connection that holds its pid.
func (l *budgetLedger) capacity(conns map[net.Conn]*session, snap types.MachineSnapshot, now time.Time) types.MachineSnapshot {
	owners := ownersOf(conns)
	held := map[int]bool{}
	snap.Holders = slices.Clone(snap.Holders)
	for i, h := range snap.Holders {
		if h.Reserved {
			held[h.PID] = true
			snap.Holders[i].Owner = l.seats[h.PID].owner
			continue
		}
		snap.Holders[i].Owner = owners[h.PID]
	}
	for pid := range l.seats {
		if !held[pid] {
			delete(l.seats, pid)
		}
	}
	for k, e := range l.waiting {
		if now.Sub(e.asked) >= waitLapse {
			delete(l.waiting, k)
			continue
		}
		snap.Waiting = append(snap.Waiting, e.waiter)
	}
	slices.SortFunc(snap.Waiting, func(a, b types.MachineWaiter) int {
		if c := a.Since.Compare(b.Since); c != 0 {
			return c
		}
		return a.PID - b.PID
	})
	snap.Refused = slices.Clone(l.refused)
	return snap
}

// wait lists w under key, keeping the first ask's time across the asks after it.
func (l *budgetLedger) wait(key waitKey, sess *session, w types.MachineWaiter, now time.Time) {
	if l.waiting == nil {
		l.waiting = map[waitKey]*waitEntry{}
	}
	w.Since = now
	if e, ok := l.waiting[key]; ok {
		w.Since = e.waiter.Since
	}
	l.waiting[key] = &waitEntry{waiter: w, sess: sess, asked: now}
}

func (l *budgetLedger) reserve(reader int, owner string, now time.Time) {
	if l.seats == nil {
		l.seats = map[int]reservedSeat{}
	}
	l.seats[reader] = reservedSeat{owner: owner, at: now}
	for len(l.seats) > reservedKeep {
		oldest := 0
		for pid, s := range l.seats {
			if oldest == 0 || s.at.Before(l.seats[oldest].at) {
				oldest = pid
			}
		}
		delete(l.seats, oldest)
	}
}

func (l *budgetLedger) refuse(r types.MachineRefusal) {
	l.refused = slices.Insert(l.refused, 0, r)
	if len(l.refused) > refusedKeep {
		l.refused = l.refused[:refusedKeep]
	}
}

func waiterOf(c types.MachineClaim, owner, tree string) types.MachineWaiter {
	return types.MachineWaiter{
		PID: c.PID, Project: c.Project, Target: c.Target, MemoryMB: c.MemoryMB, Slots: max(c.Slots, 1),
		Dir: c.Dir, Command: c.Command, Owner: owner, Tree: tree,
	}
}

func refusalOf(c types.MachineClaim, owner string, v types.MachineVerdict, owners map[int]string, now time.Time) types.MachineRefusal {
	holders := slices.Clone(v.Holders)
	for i, h := range holders {
		holders[i].Owner = owners[h.PID]
	}
	return types.MachineRefusal{
		At: now, PID: c.PID, Project: c.Project, Target: c.Target, MemoryMB: c.MemoryMB, Slots: max(c.Slots, 1),
		Dir: c.Dir, Command: c.Command, Owner: owner, Fits: v.Fits, Holders: holders,
	}
}

// sessionOwner names who answers for sess, as [Work.Owner] does: its hello's lease, else
// the lease or spawner of a command it runs under a deadline. Callers hold s.mu.
func sessionOwner(sess *session) string {
	w := Work{Lease: sess.hello.Lease}
	if w.Lease == "" {
		for _, hw := range sess.holds {
			if hw.hold.Claim == "" && (hw.work.Lease != "" || hw.work.Spawner != "") {
				w.Lease, w.Spawner = hw.work.Lease, hw.work.Spawner
				break
			}
		}
	}
	return w.Owner()
}

// ownersOf maps each connected process to its owner. A process with two connections
// takes the owner of one that names a lease or spawner. Callers hold s.mu.
func ownersOf(conns map[net.Conn]*session) map[int]string {
	out := map[int]string{}
	for _, sess := range conns {
		if sess == nil {
			continue
		}
		owner := sessionOwner(sess)
		if prev, ok := out[sess.hello.PID]; !ok || prev == (Work{}).Owner() {
			out[sess.hello.PID] = owner
		}
	}
	return out
}
