package mergequeue

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// fakeStager names a stage commit by what it stacks: "base+1+2" is base, then #1, then #2.
type fakeStager struct {
	paths     map[string][]string
	conflicts map[[2]string][]string // {"base" or an earlier id, id} -> conflicting paths
	mu        sync.Mutex
	builds    []string
	discarded int
}

func newStager(paths map[string][]string) *fakeStager {
	return &fakeStager{paths: paths, conflicts: map[[2]string][]string{}}
}

func (s *fakeStager) Tip(context.Context, string) (string, error) { return "base", nil }
func (s *fakeStager) Fetch(context.Context, Change) error         { return nil }
func (s *fakeStager) Changed(_ context.Context, _, head string) ([]string, error) {
	return s.paths[strings.TrimPrefix(head, "sha-")], nil
}

func (s *fakeStager) Overlap(_ context.Context, _ string, c Change) error {
	if p, ok := s.conflicts[[2]string{"base", c.ID}]; ok {
		return &ConflictError{Conflict: Conflict{Change: c, Paths: p, With: []string{"abc123 an earlier change"}}}
	}
	return nil
}

func (s *fakeStager) Build(_ context.Context, _, on string, c Change) (Stage, error) {
	for _, below := range strings.Split(on, "+")[1:] {
		if p, ok := s.conflicts[[2]string{below, c.ID}]; ok {
			return Stage{}, &ConflictError{Conflict: Conflict{Change: c, Paths: p}}
		}
	}
	commit := on + "+" + c.ID
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

func (s *fakeStager) Message(_ context.Context, _, head string) (string, error) {
	return "* " + head, nil
}

// fakeGate turns a stage red when the change it adds is bad, holding each gate long
// enough for concurrent ones to overlap.
type fakeGate struct {
	bad     map[string]bool
	hold    time.Duration
	wait    map[string]chan struct{} // a change's gate blocks until its channel closes
	done    map[string]chan struct{} // closed when a change's gate returns
	running atomic.Int32
	peak    atomic.Int32
	mu      sync.Mutex
	gated   []string
}

func newGate() *fakeGate { return &fakeGate{bad: map[string]bool{}, hold: 20 * time.Millisecond} }

func (g *fakeGate) Validate(ctx context.Context, s Stage, below string, c Change) (GateResult, error) {
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
	if g.bad[c.ID] {
		return GateResult{Summary: "tests failed in " + c.ID}, nil
	}
	return GateResult{Green: true}, nil
}

// readHost answers the reads planning makes; any write fails, since planning must never
// write to the forge.
type readHost struct {
	approval func(c Change) Approval
	calls    []string
}

func (h *readHost) Name() string                                      { return "fake" }
func (h *readHost) List(context.Context, ListQuery) ([]Change, error) { return nil, nil }

func (h *readHost) ApprovalAt(_ context.Context, c Change, sha string) (Approval, error) {
	h.calls = append(h.calls, "approval "+c.ID)
	if h.approval != nil {
		return h.approval(c), nil
	}
	return Approval{Approved: true, Head: sha}, nil
}

var errPlanningWrote = errors.New("planning must not write to the forge")

func (h *readHost) PostStatus(context.Context, Change, string, Status) error {
	return errPlanningWrote
}

func (h *readHost) Merge(context.Context, Change, string, string) error {
	return errPlanningWrote
}

func (h *readHost) KickBack(context.Context, Change, string, string) error {
	return errPlanningWrote
}

// change builds a change whose head is "sha-<id>" affecting units.
func change(id string, units ...string) Change {
	return Change{ID: id, Head: "sha-" + id, Base: "main", Affected: units}
}
