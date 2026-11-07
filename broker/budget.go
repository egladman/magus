package broker

import (
	"fmt"
	"net"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// maxOvertakes is how many requests may go ahead of a parked one: a smaller
// claim fitting freed room, or a heavier job's. Past it, everything behind the
// head is judged as if the head held its seat.
const maxOvertakes = 4

// outcome is what one try of a parked request came to.
type outcome int

const (
	// waiting: the budget cannot answer it yet.
	waiting outcome = iota
	// seated: answered, and it took room.
	seated
	// answered: a refusal, which takes no room.
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
	// why is what kept it out at its last try. sent is the key of the last
	// parked frame the client heard.
	why       parkedReply
	sent      string
	overtaken int
	// gate is a claim run under a gate, which queues ahead of every other request.
	gate bool
	// seq numbers the frames p is due, under s.mu. frameMu orders their
	// writes, so a frame older than one already written is dropped.
	seq     uint64
	frameMu sync.Mutex
	wrote   uint64
}

// send writes the frame numbered seq unless a newer one went out first.
func (p *parked) send(seq uint64, typ string, body any) {
	p.frameMu.Lock()
	defer p.frameMu.Unlock()
	if seq <= p.wrote {
		return
	}
	p.wrote = seq
	_ = p.w.write(typ, p.id, body)
}

// nextFrameLocked numbers the next frame p is due. Callers hold s.mu.
func (p *parked) nextFrameLocked() uint64 {
	p.seq++
	return p.seq
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
	s.markGateLocked(p.sess, p.claim)
	p.gate = s.gateLocked(p.sess, p.claim) != nil
	r, o := s.judgeLocked(p, s.aheadLocked(p, false))
	why := p.why
	var seq uint64
	if o == waiting {
		p.sent = why.key()
		seq = p.nextFrameLocked()
		s.enqueueLocked(p)
		s.armWakeLocked()
	}
	others := o == seated && len(s.queue) > 0
	s.mu.Unlock()
	if o == waiting {
		// A wake may answer p before this frame goes out; the client then drops it.
		p.send(seq, typeParked, why)
		return
	}
	_ = p.w.write(r.typ, p.id, r.body)
	if others {
		// p is a new holder the parked requests have not heard of.
		s.wake()
	}
}

// key is the whole frame, so a wake re-sends one whenever anything the
// client prints from it changed.
func (r parkedReply) key() string {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Sprintf("%+v", r)
	}
	return string(b)
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
// One still waiting whose holders changed hears a fresh parked frame.
func (s *server) wake() {
	type answer struct {
		p   *parked
		seq uint64
		r   reply
	}
	var out []answer
	s.mu.Lock()
	for i := 0; i < len(s.queue); {
		p := s.queue[i]
		r, o := s.judgeLocked(p, s.aheadLocked(p, i == 0))
		if o == waiting {
			if k := p.why.key(); k != p.sent {
				p.sent = k
				out = append(out, answer{p, p.nextFrameLocked(), reply{typeParked, p.why}})
			}
			i++
			continue
		}
		s.queue = slices.Delete(s.queue, i, i+1)
		out = append(out, answer{p, p.nextFrameLocked(), r})
	}
	s.armWakeLocked()
	s.mu.Unlock()
	for _, a := range out {
		a.p.send(a.seq, a.r.typ, a.r.body)
	}
}

// armWakeLocked sets the wake timer to the next instant a curve steps down.
// A request waiting on that is answered without a release. Callers hold s.mu.
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
			Project: c.Project, Target: c.Target, PID: c.PID, MemoryMB: c.MemoryMB,
			Slots: max(c.Slots, 1), Dir: c.Dir, Command: c.Command, Owner: sessionOwner(p.sess), Tree: tree,
			Since: p.since, WaitedMS: now.Sub(p.since).Milliseconds(),
		})
	}
	slices.SortStableFunc(out, func(a, b types.MachineWaiter) int { return a.Since.Compare(b.Since) })
	return out
}

// claimTry seats c for sess, parking it while anything a release can clear
// keeps it out. Only a claim larger than the whole budget is refused at once.
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
		case v.Fits:
			v.Holders = ownedHolders(s.conns, v.Holders)
			p.why = parkedReply{Verdict: v}
			return reply{}, waiting
		}
		s.ledger.claimed(s.conns, sess, c, v, time.Now())
		return reply{typeClaimReply, claimReply{Verdict: v}}, answered
	}
}

// ownedHolders is holders with each one's owner named. Callers hold s.mu.
func ownedHolders(conns map[net.Conn]*session, holders []types.MachineClaimant) []types.MachineClaimant {
	owners := connectionOwners(conns)
	out := slices.Clone(holders)
	for i, h := range out {
		out[i].Owner = owners[h.PID]
	}
	return out
}

// refusedKeep bounds the refusals a broker remembers for status.
const refusedKeep = 20

// budgetLedger is the last MGS3009 refusals the broker answered, bounded.
// The zero value is ready; s.mu guards it.
type budgetLedger struct {
	// refused is newest first.
	refused []types.MachineAdmission
}

// claimed records a claim's verdict when it is the MGS3009 refusal the client turns it
// into: neither a grant nor a wait on the caller's own run.
func (l *budgetLedger) claimed(conns map[net.Conn]*session, sess *session, c types.MachineClaim, v types.MachineVerdict, now time.Time) {
	if v.Granted || v.Fits && v.OwnRun {
		return
	}
	l.refuse(machineRefusal(c, sessionOwner(sess), v, connectionOwners(conns), now))
}

// capacity is snap with each holder's owner, the connection that holds its
// pid, and the refusals remembered.
func (l *budgetLedger) capacity(conns map[net.Conn]*session, snap types.MachineSnapshot) types.MachineSnapshot {
	snap.Holders = ownedHolders(conns, snap.Holders)
	snap.Refused = slices.Clone(l.refused)
	return snap
}

func (l *budgetLedger) refuse(r types.MachineAdmission) {
	l.refused = slices.Insert(l.refused, 0, r)
	if len(l.refused) > refusedKeep {
		l.refused = l.refused[:refusedKeep]
	}
}

func machineRefusal(c types.MachineClaim, owner string, v types.MachineVerdict, owners map[int]string, now time.Time) types.MachineAdmission {
	holders := slices.Clone(v.Holders)
	for i, h := range holders {
		holders[i].Owner = owners[h.PID]
	}
	return types.MachineAdmission{
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
