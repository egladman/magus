package cache

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/types"
)

// The wait board: which in-flight step holds which waiting steps, drawn in the
// band. RunAll feeds it two band-only records, gated like cache.pool, so a
// pipe, CI log or -o json run sees nothing new.
const (
	planEvent = "cache.plan"
	stepEvent = "cache.step"
)

// waitEdgeKey carries a waitForUpstream record's edge on its context, so a
// handler whose board shows that waiter can drop the line. On the context
// rather than an attribute, every output format stays as it was.
type waitEdgeKey struct{}

// StepState is where a step of a RunAll batch stands.
type StepState string

const (
	// StepWaiting is the state every planned step starts in: its upstreams
	// are not all done.
	StepWaiting StepState = "waiting"
	// StepQueued waits for a machine seat or a local slot.
	StepQueued StepState = "queued"
	// StepRunning holds its slot.
	StepRunning StepState = "running"
	// StepDone finished, whatever the verdict.
	StepDone StepState = "done"
)

// PlannedStep is one step of a cache.plan record and the batch steps it waits
// on.
type PlannedStep struct {
	Ref       types.TargetRef   `json:"ref"`
	Upstreams []types.TargetRef `json:"upstreams,omitempty"`
}

// WaitEdge is one wait: Waiter waits for Upstream.
type WaitEdge struct {
	Waiter   types.TargetRef `json:"waiter"`
	Upstream types.TargetRef `json:"upstream"`
}

// bandHandler reports the handler that draws a band, or nil when there is
// none to feed.
func (c *Cache) bandHandler() *PrettyHandler {
	ph, ok := c.log.Handler().(*PrettyHandler)
	if !ok || !ph.claimBand() {
		return nil
	}
	return ph
}

// claimBand reports whether h can draw a band, re-acquiring one an earlier
// run's EndRun gave back. RendersBand answers false in that gap, which is
// exactly when RunAll announces its plan.
func (h *PrettyHandler) claimBand() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ensureLease()
	return h.lease.Enabled()
}

// logPlan announces a batch to the board.
func (c *Cache) logPlan(ctx context.Context, steps []Step) {
	if c.bandHandler() == nil {
		return
	}
	ups := inScopeUpstreams(steps)
	plan := make([]PlannedStep, 0, len(steps))
	for _, s := range steps {
		ref := stepRef(s)
		plan = append(plan, PlannedStep{Ref: ref, Upstreams: ups[ref]})
	}
	c.log.LogAttrs(ctx, slog.LevelInfo, planEvent, slog.Any("steps", plan))
}

// logStep reports a step's move to state.
func (c *Cache) logStep(ctx context.Context, s Step, state StepState) {
	if c.bandHandler() == nil {
		return
	}
	c.log.LogAttrs(ctx, slog.LevelInfo, stepEvent,
		slog.String("project", s.ProjectPath),
		slog.String("target", s.Target),
		slog.String("state", string(state)))
}

// waitShowAfter keeps a wait off the board until it outlasts a cache hit.
// Shorter, and a cached run flickers rows that are gone before they can be
// read.
const waitShowAfter = 3 * time.Second

type boardStep struct {
	ref       types.TargetRef
	upstreams []types.TargetRef
	state     StepState
	since     time.Time
	order     int
}

// waitBoard folds plan and step records into the steps a run has in flight.
// It is pure data: the handler owns the clock and the lock.
type waitBoard struct {
	steps map[types.TargetRef]*boardStep
}

// plan adds a batch. A nested batch adds to the board rather than replacing
// it, because the outer batch's steps are still in flight.
func (b *waitBoard) plan(now time.Time, steps []PlannedStep) {
	if b.steps == nil {
		b.steps = make(map[types.TargetRef]*boardStep, len(steps))
	}
	for _, p := range steps {
		if old, ok := b.steps[p.Ref]; ok && old.state != StepDone {
			continue
		}
		b.steps[p.Ref] = &boardStep{
			ref:       p.Ref,
			upstreams: p.Upstreams,
			state:     StepWaiting,
			since:     now,
			order:     len(b.steps),
		}
	}
}

// set moves ref to state. A ref the board never planned is ignored: a step
// run outside RunAll has no edges to draw.
func (b *waitBoard) set(now time.Time, ref types.TargetRef, state StepState) {
	s, ok := b.steps[ref]
	if !ok || s.state == state {
		return
	}
	s.state, s.since = state, now
}

func (b *waitBoard) reset() { b.steps = nil }

// inFlight reports whether any planned step has not finished.
func (b *waitBoard) inFlight() bool {
	for _, s := range b.steps {
		if s.state != StepDone {
			return true
		}
	}
	return false
}

// progress counts the steps finished and planned, for the status row.
func (b *waitBoard) progress() (done, total int) {
	for _, s := range b.steps {
		if s.state == StepDone {
			done++
		}
	}
	return done, len(b.steps)
}

// knows reports whether ref is a step the board is tracking.
func (b *waitBoard) knows(ref types.TargetRef) bool {
	s, ok := b.steps[ref]
	return ok && s.state != StepDone
}

// waitNode is one row of the drawn board.
type waitNode struct {
	ref   types.TargetRef
	state StepState
	// age is how long the step has been in its state.
	age time.Duration
	// others counts the pending upstreams this waiter is not drawn under.
	others int
	// holds counts the waiters beneath this node, transitively.
	holds    int
	children []*waitNode
}

// tree returns each step holding waiters with the chain waiting behind it,
// most waiters first, so the first root is the bottleneck. Nil until a wait
// passes waitShowAfter. A waiter is drawn once, so no count is inflated.
func (b *waitBoard) tree(now time.Time) []*waitNode {
	parent := make(map[types.TargetRef]types.TargetRef)
	others := make(map[types.TargetRef]int)
	shown := false
	for _, s := range b.steps {
		if s.state != StepWaiting {
			continue
		}
		pending := b.pending(s)
		if len(pending) == 0 {
			continue
		}
		parent[s.ref] = b.pickParent(pending)
		others[s.ref] = len(pending) - 1
		if now.Sub(s.since) >= waitShowAfter {
			shown = true
		}
	}
	if !shown {
		return nil
	}
	kids := make(map[types.TargetRef][]types.TargetRef)
	for w, p := range parent {
		kids[p] = append(kids[p], w)
	}
	var build func(ref types.TargetRef) *waitNode
	build = func(ref types.TargetRef) *waitNode {
		s := b.steps[ref]
		n := &waitNode{ref: ref, state: s.state, age: now.Sub(s.since), others: others[ref]}
		for _, k := range kids[ref] {
			c := build(k)
			n.holds += 1 + c.holds
			n.children = append(n.children, c)
		}
		b.sortNodes(n.children)
		return n
	}
	var roots []*waitNode
	for ref := range kids {
		// A waiter with no pending upstream is about to queue: it heads its
		// own chain rather than dropping out of the picture.
		if _, waits := parent[ref]; waits {
			continue
		}
		roots = append(roots, build(ref))
	}
	b.sortNodes(roots)
	return roots
}

// pending is s's upstreams the board has not seen finish.
func (b *waitBoard) pending(s *boardStep) []*boardStep {
	var out []*boardStep
	for _, u := range s.upstreams {
		if up, ok := b.steps[u]; ok && up.state != StepDone {
			out = append(out, up)
		}
	}
	return out
}

// stateRank orders the states by how close a step is to finishing.
var stateRank = map[StepState]int{StepRunning: 0, StepQueued: 1, StepWaiting: 2}

// pickParent draws a waiter under the upstream closest to finishing. Ties
// keep the declared order, so a repaint never moves a row.
func (b *waitBoard) pickParent(pending []*boardStep) types.TargetRef {
	best := pending[0]
	for _, p := range pending[1:] {
		if stateRank[p.state] < stateRank[best.state] {
			best = p
		}
	}
	return best.ref
}

func (b *waitBoard) sortNodes(ns []*waitNode) {
	slices.SortFunc(ns, func(x, y *waitNode) int {
		if c := cmp.Compare(y.holds, x.holds); c != 0 {
			return c
		}
		return cmp.Compare(b.steps[x.ref].order, b.steps[y.ref].order)
	})
}

// waitBandRows is the board's share of the band, on top of the failures'.
const waitBandRows = 6

// boardShowsWait reports whether the record logged with ctx is a wait line
// the band already draws. Callers hold h.mu.
func (h *PrettyHandler) boardShowsWait(ctx context.Context) bool {
	edge, ok := ctx.Value(waitEdgeKey{}).(WaitEdge)
	return ok && h.lease.Enabled() && h.board.knows(edge.Waiter)
}

// waitLines draws the board's tree in at most budget rows. Past it the last
// row counts the waiters left out.
func waitLines(roots []*waitNode, budget int, spin string, width int, color bool, dim tty.SGR) []tty.Line {
	if len(roots) == 0 || budget <= 0 {
		return nil
	}
	var all []bandRow
	// seen[i] is the waiters drawn through row i.
	var seen []int
	waiters := 0
	var walk func(n *waitNode, indent string, last, root bool)
	walk = func(n *waitNode, indent string, last, root bool) {
		all = append(all, waitRow(n, indent, last, root, root && n == roots[0], spin, color))
		next := indent + "  "
		if !root {
			waiters++
			next = indent + "│ "
			if last {
				next = indent + "  "
			}
		}
		seen = append(seen, waiters)
		for i, c := range n.children {
			walk(c, next, i == len(n.children)-1, false)
		}
	}
	for _, r := range roots {
		walk(r, "  ", false, true)
	}
	more := 0
	if len(all) > budget {
		keep := budget - 1
		more = waiters
		if keep > 0 {
			more -= seen[keep-1]
		}
		all = all[:keep]
	}
	lines := alignBlock(all, width, tty.SGRReverse, dim)
	if more > 0 {
		lines = append(lines, tty.Line{Text: fmt.Sprintf("    +%d more waiting", more), Style: dim})
	}
	return lines
}

// waitRow is one row of the board: a holder with how many it holds, or a
// waiter with how long it has waited.
func waitRow(n *waitNode, indent string, last, root, top bool, spin string, color bool) bandRow {
	var label, mark tty.SGR
	if color {
		mark = colYellow
	}
	age := FormatDuration(n.age)
	row := bandRow{failure: -1}
	if root {
		lead := " "
		if n.state != StepWaiting {
			lead = spin
		}
		meta := []string{age}
		if n.state != StepRunning {
			meta[0] = fmt.Sprintf("%s %s", n.state, age)
		}
		if top && n.holds > 1 {
			meta = append(meta, "bottleneck")
			if color {
				label = tty.SGRBold
			}
		}
		meta = append(meta, fmt.Sprintf("holds %d", n.holds))
		row.label = []tty.Span{{Text: indent}, {Text: lead, Style: mark}, {Text: " "}}
		row.meta = strings.Join(meta, metaSep)
	} else {
		branch := treeTee
		if last {
			branch = treeEnd
		}
		meta := []string{fmt.Sprintf("%s %s", n.state, age)}
		if n.others > 0 {
			meta = append(meta, fmt.Sprintf("+%d more", n.others))
		}
		row.label = []tty.Span{{Text: indent + branch}}
		row.meta = strings.Join(meta, metaSep)
	}
	row.label = append(row.label, tty.Span{Text: displayNodeLabel(n.ref), Style: label})
	return row
}

// startTicker repaints the band every tickEvery until nothing is in flight or
// stopTicker. Callers hold h.mu.
func (h *PrettyHandler) startTicker() {
	if h.tickEvery <= 0 || h.tickStop != nil || !h.lease.Enabled() {
		return
	}
	stop := make(chan struct{})
	h.tickStop = stop
	go h.tick(stop, h.tickEvery)
}

func (h *PrettyHandler) tick(stop <-chan struct{}, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		h.mu.Lock()
		// Stopped while this tick waited for the lock.
		select {
		case <-stop:
			h.mu.Unlock()
			return
		default:
		}
		if !h.lease.Enabled() || !h.inFlightLocked() {
			h.stopTicker()
			h.mu.Unlock()
			return
		}
		h.repaint()
		h.mu.Unlock()
	}
}

// stopTicker ends the repaint loop without waiting for it, so a caller
// holding h.mu cannot deadlock against a tick. Callers hold h.mu.
func (h *PrettyHandler) stopTicker() {
	if h.tickStop != nil {
		close(h.tickStop)
		h.tickStop = nil
	}
}
