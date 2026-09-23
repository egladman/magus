package mergequeue

// Fakes shared by the step tests: a gate, a facts hook and a verdict sink, and the
// helpers that name changes. The version control and the provider are the model in
// model_test.go and modelprovider_test.go.

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// base is the plan's base commit in the tests that need no repository.
var base = strings.Repeat("b", 40)

// head is a commit id for change id in the tests that need no repository.
func head(id string) string { return fmt.Sprintf("%040s", hex.EncodeToString([]byte(id))) }

// change builds a change on main affecting units, for the tests that need no repository.
func change(id string, units ...string) Change {
	return Change{ID: id, Head: head(id), Base: "main", Method: MethodSquash, Affected: units}
}

// fakeGate turns a candidate red when the change it adds is bad, holding each gate
// long enough for concurrent ones to overlap.
type fakeGate struct {
	bad     map[string]bool
	broken  map[string]bool // the gate cannot run for these: an error, not a verdict
	hold    time.Duration
	wait    map[string]chan struct{} // a change's gate blocks until its channel closes
	done    map[string]chan struct{} // closed when a change's gate returns
	running atomic.Int32
	peak    atomic.Int32
	mu      sync.Mutex
	gated   []string // change ids, in the order their gates started
}

func newGate() *fakeGate {
	return &fakeGate{bad: map[string]bool{}, broken: map[string]bool{}, hold: 20 * time.Millisecond}
}

func (g *fakeGate) Validate(ctx context.Context, _ Candidate, _ string, c Change) (GateResult, error) {
	n := g.running.Add(1)
	defer g.running.Add(-1)
	for {
		p := g.peak.Load()
		if n <= p || g.peak.CompareAndSwap(p, n) {
			break
		}
	}
	g.mu.Lock()
	g.gated = append(g.gated, c.ID)
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

// factsFunc is BuildFacts from a function.
type factsFunc func(ctx context.Context, c Change, paths []string) ([]string, string, error)

func (f factsFunc) Affected(ctx context.Context, c Change, paths []string) ([]string, string, error) {
	return f(ctx, c, paths)
}

// collected gathers verdicts in the order validation decided them.
type collected struct {
	mu  sync.Mutex
	all []Verdict
}

func (c *collected) Record(_ context.Context, v Verdict) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.all = append(c.all, v)
	return nil
}

func (c *collected) get(t *testing.T, id string) Verdict {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, v := range c.all {
		if v.Change.ID == id {
			return v
		}
	}
	require.Failf(t, "no verdict", "#%s", id)
	return Verdict{}
}

func (c *collected) decisions() map[string]Decision {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]Decision{}
	for _, v := range c.all {
		out[v.Change.ID] = v.Decision
	}
	return out
}

// Poll hands everything collected to an Applier at once, as a finished run would.
func (c *collected) Poll(context.Context) ([]Verdict, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.all
	c.all = nil
	return out, true, nil
}

func ids(groups [][]Change) [][]string {
	var out [][]string
	for _, g := range groups {
		var row []string
		for _, c := range g {
			row = append(row, c.ID)
		}
		out = append(out, row)
	}
	return out
}
