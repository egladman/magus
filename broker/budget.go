package broker

import (
	"fmt"
	"net"
	"os"
	"slices"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/types"
)

// maxOvertakes is how many requests a release may seat ahead of the request at the head
// of the queue. A smaller claim that fits the room a release freed goes first, which
// keeps the host busy; once this many have, everything behind the head is judged as if
// the head already held its seat, so a stream of small claims cannot starve a large one.
const maxOvertakes = 4

// outcome is what one try of a parked request came to.
type outcome int

const (
	// waiting: the budget cannot answer it yet.
	waiting outcome = iota
	// seated: answered, and it took room.
	seated
	// answered: answered without taking room: a refusal, or an await that holds nothing.
	answered
)

// parked is a request the broker holds until the budget can answer it, on the connection
// that asked. A closed connection drops it.
type parked struct {
	sess  *session
	w     *frameWriter
	id    uint64
	kind  string
	since time.Time
	// claim is what it waits to seat, for status and for the seat it is judged to hold
	// once starved.
	claim types.MachineClaim
	// try answers it when the budget can. Callers hold s.mu.
	try func(p *parked) (reply, outcome)
	// why is what kept it out at its last try.
	why       parkedReply
	overtaken int
}

type reply struct {
	typ  string
	body any
}

// park answers p at once when the budget can; otherwise it queues p and says so with a
// parked frame, and the reply follows under the same id. A broker that is draining or
// stopping refuses it.
func (s *server) park(p *parked) {
	s.mu.Lock()
	if s.draining || s.closing {
		s.mu.Unlock()
		_ = p.w.write(typeError, p.id, s.drainingError())
		return
	}
	r, o := s.judgeLocked(p, s.starvedHeadLocked())
	why := p.why
	if o == waiting {
		s.queue = append(s.queue, p)
		s.armWakeLocked()
	}
	s.mu.Unlock()
	if o == waiting {
		// A wake may answer p before this frame goes out; the client then drops it.
		_ = p.w.write(typeParked, p.id, why)
		return
	}
	_ = p.w.write(r.typ, p.id, r.body)
}

// starvedHeadLocked is the head of the queue once maxOvertakes requests went ahead of it,
// nil otherwise. Callers hold s.mu.
func (s *server) starvedHeadLocked() *parked {
	if len(s.queue) == 0 || s.queue[0].overtaken < maxOvertakes {
		return nil
	}
	return s.queue[0]
}

// judgeLocked tries p. Behind a starved head it is judged with the head's claim recorded,
// so it is seated only where it leaves the head its room, and a claim the head now keeps
// out is refused or waits by the same rule as one any holder keeps out. Callers hold s.mu.
func (s *server) judgeLocked(p, starved *parked) (reply, outcome) {
	if starved == nil || starved == p {
		r, o := p.try(p)
		if o == seated && len(s.queue) > 0 && s.queue[0] != p {
			s.queue[0].overtaken++
		}
		return r, o
	}
	head := starved.claim
	head.Slots = max(head.Slots, 1)
	id := s.budget.Assert(head)
	defer s.budget.Release(id)
	return p.try(p)
}

// wake tries every parked request in order, answering each the budget now can. It runs
// after anything that frees room and when the wake timer fires.
func (s *server) wake() {
	type answer struct {
		p *parked
		r reply
	}
	var out []answer
	s.mu.Lock()
	for i := 0; i < len(s.queue); {
		p := s.queue[i]
		starved := s.starvedHeadLocked()
		if i == 0 {
			starved = nil
		}
		r, o := s.judgeLocked(p, starved)
		if o == waiting {
			i++
			continue
		}
		s.queue = slices.Delete(s.queue, i, i+1)
		out = append(out, answer{p, r})
	}
	s.armWakeLocked()
	s.mu.Unlock()
	for _, a := range out {
		_ = a.p.w.write(a.r.typ, a.p.id, a.r.body)
	}
}

// armWakeLocked sets the wake timer to the next instant the budget changes on its own (a
// reservation lapsing, a curve stepping down), so a request waiting on that is answered
// without a release. A reservation whose reader exits early lapses at its bound. Callers
// hold s.mu.
func (s *server) armWakeLocked() {
	if s.wakeTimer != nil {
		s.wakeTimer.Stop()
		s.wakeTimer = nil
	}
	if len(s.queue) == 0 || s.closing {
		return
	}
	if at, ok := s.budget.NextChange(); ok {
		s.wakeTimer = time.AfterFunc(time.Until(at), s.wake)
	}
}

// cancel drops sess's parked request id, answering it with CodeCancelled so the client's
// wait for it ends.
func (s *server) cancel(sess *session, id uint64) {
	s.mu.Lock()
	i := slices.IndexFunc(s.queue, func(p *parked) bool { return p.sess == sess && p.id == id })
	var p *parked
	if i >= 0 {
		p = s.queue[i]
		s.queue = slices.Delete(s.queue, i, i+1)
	}
	s.mu.Unlock()
	if p != nil {
		_ = p.w.write(typeError, id, errorReply{Code: CodeCancelled, Message: fmt.Sprintf("broker: the %s waited %s and was cancelled", p.kind, time.Since(p.since).Round(time.Millisecond))})
		s.wake()
	}
}

// dropLocked removes every request sess has parked, for a connection that closed.
// Callers hold s.mu.
func (s *server) dropLocked(sess *session) {
	s.queue = slices.DeleteFunc(s.queue, func(p *parked) bool { return p.sess == sess })
}

// refuseParked answers every parked request with an error frame and empties the queue,
// for a broker that will seat nothing more.
func (s *server) refuseParked(code ErrorCode, why string) {
	s.mu.Lock()
	queue := s.queue
	s.queue = nil
	s.armWakeLocked()
	s.mu.Unlock()
	for _, p := range queue {
		_ = p.w.write(typeError, p.id, errorReply{Code: code, Message: fmt.Sprintf(
			"the broker (pid %d) %s; this %s waited %s and was not seated", os.Getpid(), why, p.kind, time.Since(p.since).Round(time.Second))})
	}
}

// waitingLocked lists the parked requests, oldest first, as status shows them, each with
// who answers for it and the job tree whose validation seat keeps it out. Callers hold
// s.mu.
func (s *server) waitingLocked(now time.Time) []types.MachineWaiter {
	out := make([]types.MachineWaiter, 0, len(s.queue))
	for _, p := range s.queue {
		c := p.claim
		tree := ""
		if p.why.Seat != nil {
			tree = p.why.Seat.Tree
		}
		out = append(out, types.MachineWaiter{
			Kind: p.kind, Project: c.Project, Target: c.Target, PID: c.PID, MemoryMB: c.MemoryMB,
			Slots: max(c.Slots, 1), Dir: c.Dir, Command: c.Command, Owner: sessionOwner(p.sess), Tree: tree,
			Since: p.since, WaitedMS: now.Sub(p.since).Milliseconds(),
		})
	}
	slices.SortStableFunc(out, func(a, b types.MachineWaiter) int { return a.Since.Compare(b.Since) })
	return out
}

// claimTry seats c for sess, parking it while only its own run or its job tree's
// validation seat keeps it out. Any other refusal answers at once: magus never waits on
// another invocation.
func (s *server) claimTry(sess *session, c types.MachineClaim) func(*parked) (reply, outcome) {
	return func(p *parked) (reply, outcome) {
		if wait := s.seatLocked(sess, c, false); wait != nil {
			p.why = parkedReply{Verdict: types.MachineVerdict{Fits: true, OwnRun: true}, Seat: wait}
			return reply{}, waiting
		}
		v := s.budget.Request(c)
		switch {
		case v.Granted:
			sess.claims[v.ID] = max(c.Slots, 1)
			return reply{typeClaimReply, claimReply{Verdict: v}}, seated
		case v.Fits && v.OwnRun:
			p.why = parkedReply{Verdict: v}
			return reply{}, waiting
		}
		s.ledger.claimed(s.conns, sess, c, v, time.Now())
		return reply{typeClaimReply, claimReply{Verdict: v}}, answered
	}
}

// reserveTry holds a seat the size of c for reader once the budget can seat it beside
// what it holds. A seat larger than the whole budget is refused at once.
func (s *server) reserveTry(c types.MachineClaim, reader int) func(*parked) (reply, outcome) {
	return func(p *parked) (reply, outcome) {
		v := s.budget.Reserve(c, reader)
		switch {
		case v.Granted:
			s.ledger.reserved(s.conns, p.sess, c, reader, v, time.Now())
			return reply{typeReserveReply, reserveReply{Verdict: v}}, seated
		case v.Fits:
			p.why = parkedReply{Verdict: v, Blocked: c}
			return reply{}, waiting
		}
		s.ledger.reserved(s.conns, p.sess, c, reader, v, time.Now())
		return reply{typeReserveReply, reserveReply{Verdict: v}}, answered
	}
}

// awaitTry answers once the budget would seat each of claims beside what it holds, and
// refuses at once a claim larger than the whole budget.
func (s *server) awaitTry(sess *session, claims []types.MachineClaim) func(*parked) (reply, outcome) {
	return func(p *parked) (reply, outcome) {
		blocked, v := cache.AwaitVerdict(s.budget.Snapshot(), claims)
		p.claim = s.attribute(sess, blocked)
		if !v.Granted && v.Fits {
			p.why = parkedReply{Verdict: v, Blocked: blocked}
			return reply{}, waiting
		}
		return reply{typeAwaitReply, awaitReply{Verdict: v, Blocked: blocked}}, answered
	}
}

// refusedKeep bounds the refusals a broker remembers for status.
const refusedKeep = 20

// reservedKeep bounds the reserved seats whose owner the ledger remembers. A seat lapses
// within the budget's reservation bound, so the oldest past this many is long gone.
const reservedKeep = 64

type reservedSeat struct {
	owner string
	at    time.Time
}

// budgetLedger is what the broker remembers about its budget beyond the claims it holds
// and the requests it parks: who asked for each reserved seat, and the last MGS3009
// refusals it answered, each bounded. The zero value is ready; s.mu guards it.
type budgetLedger struct {
	seats map[int]reservedSeat
	// refused is newest first.
	refused []types.MachineRefusal
}

// claimed records a claim's verdict when it is the MGS3009 refusal the client turns it
// into: neither a grant nor a wait on the caller's own run.
func (l *budgetLedger) claimed(conns map[net.Conn]*session, sess *session, c types.MachineClaim, v types.MachineVerdict, now time.Time) {
	if v.Granted || v.Fits && v.OwnRun {
		return
	}
	l.refuse(machineRefusal(c, sessionOwner(sess), v, connectionOwners(conns), now))
}

// reserved records what a reservation's verdict says about the run it is asked for,
// reader: a grant is a seat the asker's owner holds, and a claim that can never fit is a
// refusal.
func (l *budgetLedger) reserved(conns map[net.Conn]*session, sess *session, c types.MachineClaim, reader int, v types.MachineVerdict, now time.Time) {
	c.PID = reader
	owner := sessionOwner(sess)
	switch {
	case v.Granted:
		l.reserve(reader, owner, now)
	case !v.Fits:
		l.refuse(machineRefusal(c, owner, v, connectionOwners(conns), now))
	}
}

// capacity is snap with each holder's owner and the refusals remembered. A reserved
// holder's owner is the one who asked for the seat; any other's is the connection that
// holds its pid.
func (l *budgetLedger) capacity(conns map[net.Conn]*session, snap types.MachineSnapshot) types.MachineSnapshot {
	owners := connectionOwners(conns)
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
	snap.Refused = slices.Clone(l.refused)
	return snap
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

func machineRefusal(c types.MachineClaim, owner string, v types.MachineVerdict, owners map[int]string, now time.Time) types.MachineRefusal {
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

// connectionOwners maps each connected process to its owner. A process with two connections
// takes the owner of one that names a lease or spawner. Callers hold s.mu.
func connectionOwners(conns map[net.Conn]*session) map[int]string {
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
