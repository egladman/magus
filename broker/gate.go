package broker

import (
	"cmp"
	"slices"
	"strconv"
	"time"

	"github.com/egladman/magus/types"
)

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

// enqueueLocked queues p: a gate behind the gates waiting, ahead of the rest;
// anything else behind every request of its weight or more. Going ahead counts
// as overtaking, so a stream of heavier jobs cannot starve a light one.
// Callers hold s.mu.
func (s *server) enqueueLocked(p *parked) {
	if p.gate {
		i := slices.IndexFunc(s.queue, func(q *parked) bool { return !q.gate })
		if i < 0 {
			i = len(s.queue)
		}
		s.queue = slices.Insert(s.queue, i, p)
		return
	}
	i := len(s.queue)
	for i > 0 {
		q := s.queue[i-1]
		if q.gate || q.overtaken >= maxOvertakes || jobWeight(q.claim) >= jobWeight(p.claim) {
			break
		}
		i--
	}
	for _, q := range s.queue[i:] {
		q.overtaken++
	}
	s.queue = slices.Insert(s.queue, i, p)
}

// jobWeight is what c weighs in the queue. A run bound to no job, and a job
// that names no weight, carry zero and weigh the default.
func jobWeight(c types.MachineClaim) float64 {
	return cmp.Or(c.Weight, types.DefaultJobWeight)
}
