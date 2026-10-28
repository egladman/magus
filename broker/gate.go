package broker

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/types"
)

// gateWaitBound is how long the oldest gate's claim waits for other invocations' seats
// before it is refused. A var so a test can shorten it.
var gateWaitBound = 30 * time.Minute

// GateWait is why a gate's claim waits: other invocations hold the seat it needs, and it
// runs under the oldest gate, so it waits for them instead of failing the gate part-way.
type GateWait struct {
	Project string                  `json:"project"`
	Target  string                  `json:"target"`
	Bound   time.Duration           `json:"bound"`
	Holders []types.MachineClaimant `json:"holders,omitempty"`
}

func (w GateWait) String() string {
	held := make([]string, 0, len(w.Holders))
	for _, h := range w.Holders {
		held = append(held, fmt.Sprintf("%s %s (pid %d, %s, %d slot(s))", h.Project, h.Target, h.PID, cmp.Or(h.Owner, "unleased"), max(h.Slots, 1)))
	}
	return fmt.Sprintf("the gate waits for a seat for %s %s, held by %s; it gives up after %s",
		w.Project, w.Target, cmp.Or(strings.Join(held, "; "), "a run that is ending"), w.Bound)
}

// markGateLocked records when sess, a gate, asked for its first claim, and the run its
// claims belong to, so the magus processes it runs are known as the gate's. Callers hold
// s.mu.
func (s *server) markGateLocked(sess *session, c types.MachineClaim) {
	if !sess.hello.Gate || !sess.gateAsked.IsZero() {
		return
	}
	sess.gateAsked = time.Now()
	sess.gateRun = cmp.Or(c.Run(), "pid "+strconv.Itoa(sess.hello.PID))
}

// gateLocked is the gate session c runs under: sess itself when it is a gate, else the
// gate whose run c runs underneath, nil for none. Callers hold s.mu.
func (s *server) gateLocked(sess *session, c types.MachineClaim) *session {
	if sess.hello.Gate {
		return sess
	}
	run := c.Run()
	if run == "" {
		return nil
	}
	for _, other := range s.conns {
		if other != nil && other.hello.Gate && other.gateRun == run {
			return other
		}
	}
	return nil
}

// oldestGateLocked is the connected gate that asked for its first claim earliest, nil for
// none. Only it waits on other invocations: two gates each holding part of the budget and
// waiting for the rest would wait on each other. Callers hold s.mu.
func (s *server) oldestGateLocked() *session {
	var oldest *session
	for _, sess := range s.conns {
		if sess == nil || !sess.hello.Gate || sess.gateAsked.IsZero() {
			continue
		}
		if oldest == nil || sess.gateAsked.Before(oldest.gateAsked) {
			oldest = sess
		}
	}
	return oldest
}

// gateWaitLocked decides a claim other invocations' seats refused: one under the oldest
// gate waits until it has waited gateWaitBound, then is refused naming the bound. ok is
// false for any other claim, which is answered as before. Callers hold s.mu.
func (s *server) gateWaitLocked(p *parked, v types.MachineVerdict) (r reply, o outcome, ok bool) {
	gate := s.gateLocked(p.sess, p.claim)
	if gate == nil || !v.Fits || gate != s.oldestGateLocked() {
		return reply{}, waiting, false
	}
	owners := connectionOwners(s.conns)
	holders := slices.Clone(v.Holders)
	for i, h := range holders {
		holders[i].Owner = owners[h.PID]
	}
	if waited := time.Since(p.since); waited >= gateWaitBound {
		return reply{typeError, errorReply{Code: CodeGateTimeout, Message: fmt.Sprintf(
			"machine budget: %s %s waited %s for a seat, the most a gate waits, and it is still held by %s",
			p.claim.Project, p.claim.Target, waited.Round(time.Second), types.MachineSnapshot{Holders: holders}.HolderList(time.Now()))}}, answered, true
	}
	p.why = parkedReply{Verdict: v, Gate: &GateWait{Project: p.claim.Project, Target: p.claim.Target, Bound: gateWaitBound, Holders: holders}}
	return reply{}, waiting, true
}

// aheadLocked is the parked request p is judged as if it already held its seat: for a p
// that is no gate, the first gate waiting, so a feature check never takes the room a gate
// waits for; else the queue's starved head. atHead is p being the queue's first entry.
// Callers hold s.mu.
func (s *server) aheadLocked(p *parked, atHead bool) *parked {
	if !p.gate {
		if i := slices.IndexFunc(s.queue, func(q *parked) bool { return q.gate && q != p }); i >= 0 {
			return s.queue[i]
		}
	}
	if atHead {
		return nil
	}
	return s.starvedHeadLocked()
}

// requestLocked is the budget's answer to an unparked claim, judged as a parked one is:
// behind the first gate waiting, when c is no gate's. Callers hold s.mu.
func (s *server) requestLocked(sess *session, c types.MachineClaim) types.MachineVerdict {
	p := &parked{sess: sess, claim: c, gate: s.gateLocked(sess, c) != nil}
	if ahead := s.aheadLocked(p, true); ahead != nil {
		head := ahead.claim
		head.Slots = max(head.Slots, 1)
		id := s.budget.Assert(head)
		defer s.budget.Release(id)
	}
	return s.budget.Request(c)
}

// enqueueLocked queues p: a gate behind the gates already waiting and ahead of every
// other request, anything else last. Callers hold s.mu.
func (s *server) enqueueLocked(p *parked) {
	if !p.gate {
		s.queue = append(s.queue, p)
		return
	}
	i := slices.IndexFunc(s.queue, func(q *parked) bool { return !q.gate })
	if i < 0 {
		i = len(s.queue)
	}
	s.queue = slices.Insert(s.queue, i, p)
}

// gateDeadlineLocked is the earliest instant a waiting gate claim reaches gateWaitBound.
// Callers hold s.mu.
func (s *server) gateDeadlineLocked() (time.Time, bool) {
	var at time.Time
	for _, p := range s.queue {
		if p.why.Gate == nil {
			continue
		}
		if d := p.since.Add(gateWaitBound); at.IsZero() || d.Before(at) {
			at = d
		}
	}
	return at, !at.IsZero()
}

// gateRefusal is a gate's claim refused at gateWaitBound, as exit 75: the cache then fails
// the step, where a bare broker error would admit it unarbitrated.
func gateRefusal(message string) error {
	return types.ExitError{Code: cache.ExitCodeMachineBusy, Err: fmt.Errorf("%s", message)}
}
