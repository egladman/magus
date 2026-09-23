package mergequeue

// Fakes shared by the step tests: a staging repo and a gate for the Validator, a
// read-only provider for the Planner, and the helpers that name commits. Heads and the
// base commit are real-looking object ids because every step checks them; stage commits
// are names ("<base>+1+2" is the base, then #1, then #2) since nothing checks those.

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// base is the plan's base commit in every fake.
var base = strings.Repeat("b", 40)

// head is the head commit of change id: the id's bytes in hex, zero-padded.
func head(id string) string { return fmt.Sprintf("%040s", hex.EncodeToString([]byte(id))) }

// idOf inverts head.
func idOf(commit string) string {
	b, err := hex.DecodeString(strings.TrimLeft(commit, "0"))
	if err != nil {
		return ""
	}
	return string(b)
}

// change builds a change on main affecting units.
func change(id string, units ...string) Change {
	return Change{ID: id, Head: head(id), Base: "main", Affected: units}
}

type fakeStager struct {
	paths     map[string][]string
	conflicts map[[2]string][]string // {"base" or an earlier id, id} -> conflicting paths
	refused   map[string]string      // id -> why staging it is refused
	mu        sync.Mutex
	builds    []string
	discarded int
}

func newStager(paths map[string][]string) *fakeStager {
	return &fakeStager{paths: paths, conflicts: map[[2]string][]string{}, refused: map[string]string{}}
}

func (s *fakeStager) FetchTip(context.Context, string) (string, error) { return base, nil }
func (s *fakeStager) FetchHead(context.Context, Change) error          { return nil }
func (s *fakeStager) ReviewTarget(_ context.Context, _, h string) (string, error) {
	return h, nil
}

func (s *fakeStager) Changed(_ context.Context, _, h string) ([]string, error) {
	return s.paths[idOf(h)], nil
}

func (s *fakeStager) CheckMerge(_ context.Context, _ string, c Change) error {
	if p, ok := s.conflicts[[2]string{"base", c.ID}]; ok {
		return &ConflictError{Conflict: Conflict{Change: c, Paths: p, With: []string{"abc123 an earlier change"}}}
	}
	return nil
}

func (s *fakeStager) Stage(_ context.Context, _, onto string, c Change) (Stage, error) {
	for _, below := range strings.Split(onto, "+")[1:] {
		if p, ok := s.conflicts[[2]string{below, c.ID}]; ok {
			return Stage{}, &ConflictError{Conflict: Conflict{Change: c, Paths: p}}
		}
	}
	if why, ok := s.refused[c.ID]; ok {
		return Stage{}, &RefusedError{Reason: why}
	}
	commit := onto + "+" + c.ID
	s.mu.Lock()
	s.builds = append(s.builds, commit)
	s.mu.Unlock()
	return Stage{Commit: commit, Dir: "/stages/" + commit}, nil
}

func (s *fakeStager) Discard(context.Context, Stage) error {
	s.mu.Lock()
	s.discarded++
	s.mu.Unlock()
	return nil
}

func (s *fakeStager) SquashMessage(_ context.Context, _, h string) (string, error) {
	return "* " + idOf(h), nil
}

// fakeGate turns a stage red when the change it adds is bad, holding each gate long
// enough for concurrent ones to overlap.
type fakeGate struct {
	bad     map[string]bool
	broken  map[string]bool // the gate cannot run for these: an error, not a verdict
	hold    time.Duration
	wait    map[string]chan struct{} // a change's gate blocks until its channel closes
	done    map[string]chan struct{} // closed when a change's gate returns
	running atomic.Int32
	peak    atomic.Int32
	mu      sync.Mutex
	gated   []string
}

func newGate() *fakeGate {
	return &fakeGate{bad: map[string]bool{}, broken: map[string]bool{}, hold: 20 * time.Millisecond}
}

func (g *fakeGate) Validate(ctx context.Context, s Stage, _ string, c Change) (GateResult, error) {
	n := g.running.Add(1)
	defer g.running.Add(-1)
	for {
		p := g.peak.Load()
		if n <= p || g.peak.CompareAndSwap(p, n) {
			break
		}
	}
	g.mu.Lock()
	g.gated = append(g.gated, s.Commit)
	g.mu.Unlock()
	if ch, ok := g.wait[c.ID]; ok {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			return GateResult{}, context.DeadlineExceeded
		}
	}
	if ch, ok := g.done[c.ID]; ok {
		defer close(ch)
	}
	select {
	case <-time.After(g.hold):
	case <-ctx.Done():
		return GateResult{}, ctx.Err()
	}
	if g.broken[c.ID] {
		return GateResult{}, errors.New("the runner ran out of memory")
	}
	if g.bad[c.ID] {
		return GateResult{Summary: "tests failed in " + c.ID}, nil
	}
	return GateResult{Green: true}, nil
}

// readHost answers the reads planning makes; any write fails, since planning must never
// write to the forge.
type readHost struct {
	mu       sync.Mutex
	approval func(c Change) Approval
	calls    []string
}

func (h *readHost) ListChanges(context.Context, ListQuery) ([]Change, error) { return nil, nil }

func (h *readHost) ApprovalAt(_ context.Context, c Change, commit string) (Approval, error) {
	h.mu.Lock()
	h.calls = append(h.calls, "approval "+c.ID)
	h.mu.Unlock()
	if h.approval != nil {
		return h.approval(c), nil
	}
	return Approval{Approved: true, Head: commit}, nil
}

var errPlanningWrote = errors.New("planning must not write to the forge")

func (h *readHost) PostStatus(context.Context, Change, string, CommitStatus) error {
	return errPlanningWrote
}

func (h *readHost) MergeChange(context.Context, Change, string, string) error {
	return errPlanningWrote
}

func (h *readHost) KickBack(context.Context, Change, string, string) error {
	return errPlanningWrote
}
