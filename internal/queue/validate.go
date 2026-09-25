package queue

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/queue/types"
)

// Validator is the read-only step of a queue run. It executes the changes' code (the
// gate and the regeneration run on candidates) and so must never hold a credential that
// can write. It calls no [types.Provider] at all, and nothing it produces is trusted further:
// an [Applier] rebuilds each candidate itself before merging it.
type Validator struct {
	// Only, when set, validates that one change: the changes beneath it in its
	// partition are merged under it but not gated, since their own runs gate them.
	// This is how a CI system spreads one plan's candidates over separate jobs.
	Only string
	// Parallel caps the candidates being built or gated at once across every partition,
	// so many disjoint partitions do not each start Depth builds. Zero means
	// runtime.NumCPU().
	Parallel int
	// Regenerate rewrites the generated files a change touches or conflicts in on each
	// candidate. Nil leaves them as merged, which is only right when nothing generates
	// them.
	Regenerate types.RegenerateFunc
	// Reproduce is the hook command lines the gate and Regenerate run, recorded on
	// every verdict so a kick-back can say how to run them again. Zero records none.
	Reproduce types.Reproduction
	Events    *Events

	vcs     types.BuildVCS
	clone   Clone
	gate    types.Gate
	dir     *VerdictDir
	facts   types.BuildFacts
	scratch string
}

// NewValidator builds candidates in cl with v under scratch, gates them with gate, and
// records each verdict in dir the moment it is decided, so an Applier can start on it
// while later candidates still run. scratch must be absolute and outside cl.Root, where
// a checkout would be discovered as a second copy of the repository.
func NewValidator(v types.BuildVCS, cl Clone, gate types.Gate, dir *VerdictDir, f types.BuildFacts, scratch string) (*Validator, error) {
	switch {
	case v == nil || gate == nil || dir == nil || f == nil:
		return nil, errors.New("validator needs a VCS, a gate, a verdict directory and build facts")
	case cl.check() != nil:
		return nil, cl.check()
	case !filepath.IsAbs(scratch):
		return nil, fmt.Errorf("scratch directory %q is not absolute", scratch)
	}
	return &Validator{vcs: v, clone: cl, gate: gate, dir: dir, facts: f, scratch: scratch}, nil
}

// Run validates plan's admitted changes, or only v.Only. Partitions run side by side,
// and an error in one stops that partition alone: the verdicts the others reach are
// still sound, and what no verdict reached waits for the next run.
func (v *Validator) Run(ctx context.Context, plan types.Plan) error {
	if v.Parallel < 0 {
		return fmt.Errorf("parallel %d must not be negative", v.Parallel)
	}
	if err := plan.Check(); err != nil {
		return err
	}
	n := v.Parallel
	if n == 0 {
		n = runtime.NumCPU()
	}
	r := &validation{Validator: v, plan: plan, slots: make(chan struct{}, n), bases: map[string]*baseGate{},
		allUnits: sync.OnceValues(func() ([]string, error) { return v.facts.AllUnits(ctx) })}
	defer func() {
		if err := removeCheckoutsUnder(context.WithoutCancel(ctx), v.vcs, v.clone.Root, v.scratch); err != nil {
			v.Events.Emit(Event{Kind: EventNotice, Reason: "remove checkouts under " + v.scratch + ": " + err.Error()})
		}
	}()
	if v.Only != "" {
		return r.only(ctx)
	}
	var wg sync.WaitGroup
	errs := make([]error, len(plan.Partitions))
	for gi, g := range plan.Partitions {
		wg.Go(func() { errs[gi] = r.pipeline(ctx, gi, g) })
	}
	wg.Wait()
	return errors.Join(errs...)
}

type validation struct {
	*Validator
	plan     types.Plan
	slots    chan struct{} // one per candidate being built or gated
	allUnits func() ([]string, error)

	mu    sync.Mutex
	bases map[string]*baseGate // by onto and units
}

// baseGate is the gate on the commit red candidates were built onto, run once for every
// change it answers.
type baseGate struct {
	once sync.Once
	red  bool
	err  error
}

// units is what the hooks run on c's candidate take: its affected set when that is a
// proof, else every unit.
func (r *validation) units(c types.Change) ([]string, error) {
	if proven(c) {
		return c.Affected, nil
	}
	all, err := r.allUnits()
	if err != nil {
		return nil, fmt.Errorf("every unit: %w", err)
	}
	return all, nil
}

type flight struct {
	change types.Change
	cand   types.Candidate // zero when building it was refused
	onto   string
	after  string // change beneath it, "" at the bottom
	depth  int
	cancel context.CancelFunc
	done   chan outcome
	// resolved names the source files auto-resolution settled in cand, "" for none.
	resolved string
}

type outcome struct {
	green   bool
	summary string
	refused *types.RefusedError // building the candidate refused the change rather than a red gate
	err     error
	took    time.Duration
}

// stackHold is the wait of a change stacked on one that is not green this run.
type stackHold struct {
	code   types.Code
	reason string
}

func waitingBelow(id string) stackHold {
	return stackHold{code: types.CodeWaitBelow, reason: "stacked on #" + id + ", which did not validate this run"}
}

// pipeline runs one partition's speculative candidates. Up to Depth are in flight, each
// built onto the one below; their gates run concurrently and are consumed in order.
// When the lowest is green its change is decided and the window slides up; when it is
// red its change is kicked back (everything beneath it validated), or waits when what
// it was built onto is red as well, and every candidate above, all built onto it, is
// rebuilt onto what did validate. A refused candidate is red the same way, and is
// attributed only once it is lowest. A
// change stacked on one that is not green this run, or whose candidate did not build,
// waits, and is never built onto what lacks the change beneath it.
func (r *validation) pipeline(ctx context.Context, group int, pending []types.Change) error {
	onto, ontoID := r.plan.BaseCommit, ""
	held := map[string]stackHold{} // changes not green this run: what their stacks wait with
	var inflight []*flight
	defer func() {
		for _, f := range inflight {
			r.ground(ctx, f)
		}
	}()
	for {
		for len(inflight) < r.plan.Depth && len(pending) > 0 {
			c := pending[0]
			pending = pending[1:]
			if h, ok := r.holdFor(c, held, inflight); ok {
				held[c.ID] = h
				if h.code != types.CodeWaitBelowKicked {
					held[c.ID] = waitingBelow(c.ID)
				}
				if err := r.decide(types.Verdict{Change: c, Decision: types.DecisionWait, Code: h.code, Reason: h.reason}); err != nil {
					return err
				}
				continue
			}
			f := &flight{change: c, onto: onto, after: ontoID, depth: len(inflight) + 1, done: make(chan outcome, 1)}
			for _, below := range slices.Backward(inflight) {
				if below.cand.Commit != "" { // a refused candidate is nothing to build onto
					f.onto, f.after = below.cand.Commit, below.change.ID
					break
				}
			}
			queued, err := r.launch(ctx, group, f)
			if err != nil {
				return err
			}
			if queued {
				inflight = append(inflight, f)
			} else {
				held[c.ID] = waitingBelow(c.ID)
			}
		}
		if len(inflight) == 0 {
			return nil
		}
		head := inflight[0]
		inflight = inflight[1:]
		out := <-head.done
		head.cancel()
		r.discard(ctx, head.cand)
		if out.err != nil {
			return fmt.Errorf("gate %s: %w", head.change.Label(), out.err)
		}
		decision, err := r.verdict(ctx, head, out, true)
		if err != nil {
			return err
		}
		green := decision == types.DecisionMerge
		switch decision {
		case types.DecisionKick:
			held[head.change.ID] = stackHold{code: types.CodeWaitBelowKicked, reason: kickedBelow(head.change.ID)}
		case types.DecisionWait:
			held[head.change.ID] = waitingBelow(head.change.ID)
		}
		if !green && head.cand.Commit == "" {
			continue // nothing above was built onto a refused candidate
		}
		if !green {
			requeue := make([]types.Change, 0, len(inflight)+len(pending))
			for _, f := range inflight {
				r.ground(ctx, f)
				requeue = append(requeue, f.change)
			}
			inflight = nil
			pending = append(requeue, pending...)
			continue
		}
		onto, ontoID = head.cand.Commit, head.change.ID
	}
}

// holdFor is the wait of c when the change it is stacked on is held this run, or is in
// flight with no candidate to build c onto.
func (r *validation) holdFor(c types.Change, held map[string]stackHold, inflight []*flight) (stackHold, bool) {
	if c.Below == "" {
		return stackHold{}, false
	}
	if h, ok := held[c.Below]; ok {
		if h.code == types.CodeWaitBelowKicked {
			return h, true
		}
		return waitingBelow(c.Below), true
	}
	for _, f := range inflight {
		if f.change.ID == c.Below && f.cand.Commit == "" {
			return waitingBelow(c.Below), true
		}
	}
	return stackHold{}, false
}

// launch builds f's candidate and starts its gate, reporting whether f joined the
// flights. A conflict or a wait is decided on the spot and leaves the window; a refused
// candidate joins with its red outcome already in hand.
func (r *validation) launch(ctx context.Context, group int, f *flight) (bool, error) {
	if err := r.acquire(ctx); err != nil {
		return false, err
	}
	cand, err := r.candidate(ctx, f)
	if err == nil {
		f.cand = cand
		r.start(ctx, group, f)
		return true, nil
	}
	r.release()
	var refused *types.RefusedError
	if errors.As(err, &refused) {
		f.cancel = func() {}
		f.done <- outcome{refused: refused, summary: refused.Reason}
		return true, nil
	}
	return false, r.hold(f, err)
}

// hold decides a wait for a build error that says nothing against the change, and
// returns the error as it stands when it is the machine's.
func (r *validation) hold(f *flight, err error) error {
	var wait *waitError
	switch conf, ok := asConflict(err); {
	case ok:
		// Planning proved it merges onto the base alone, so it conflicts with a change
		// ahead of it. The next run sees that change merged.
		return r.decide(types.Verdict{Change: f.change, Decision: types.DecisionWait, Code: types.CodeWaitConflictAhead,
			Reason: conflictAhead(f.after, conf), Paths: conf.paths})
	case errors.As(err, &wait):
		return r.decide(types.Verdict{Change: f.change, Decision: types.DecisionWait, Code: wait.code, Reason: wait.reason})
	}
	return fmt.Errorf("build the candidate of %s onto %s: %w", f.change.Label(), short(f.onto), err)
}

// only builds v.Only's chain one candidate at a time and gates just the top. What is
// red on top of changes this run did not gate cannot be pinned on the top change, so it
// waits for a run where they are validated rather than kicking its author back.
func (r *validation) only(ctx context.Context) error {
	gi, pos, ok := find(r.plan, r.Only)
	if !ok {
		return fmt.Errorf("%s is not an admitted change of the plan", r.Only)
	}
	onto, after := r.plan.BaseCommit, ""
	var built []types.Candidate
	defer func() {
		for _, cand := range built {
			r.discard(ctx, cand)
		}
	}()
	skipped := map[string]bool{}
	for i, c := range r.plan.Partitions[gi][:pos+1] {
		f := &flight{change: c, onto: onto, after: after, depth: len(built) + 1, done: make(chan outcome, 1)}
		if skipped[c.Below] {
			if i < pos {
				skipped[c.ID] = true
				continue
			}
			h := waitingBelow(c.Below)
			return r.decide(types.Verdict{Change: c, Decision: types.DecisionWait, Code: h.code, Reason: h.reason})
		}
		cand, err := r.candidate(ctx, f)
		if err != nil {
			if i < pos {
				skipped[c.ID] = true
				continue // its own run decides it; the chain skips it, as the pipeline does
			}
			var refused *types.RefusedError
			if errors.As(err, &refused) {
				_, err := r.verdict(ctx, f, outcome{refused: refused, summary: refused.Reason}, len(built) == 0)
				return err
			}
			return r.hold(f, err)
		}
		built = append(built, cand)
		if i < pos {
			onto, after = cand.Commit, c.ID
			continue
		}
		f.cand = cand
		if err := r.acquire(ctx); err != nil {
			return err
		}
		r.start(ctx, gi, f)
		out := <-f.done
		f.cancel()
		if out.err != nil {
			return fmt.Errorf("gate %s: %w", c.Label(), out.err)
		}
		_, err = r.verdict(ctx, f, out, f.depth == 1)
		return err
	}
	return nil
}

// candidate builds f's change's candidate onto f.onto, regenerated with v.Regenerate.
//
// What the regeneration rewrites is committed only when the build tool proves it runs
// none of c's code, the same proof an Applier needs before it can reproduce those bytes
// with the base's own regeneration. Otherwise c's committed outputs are stale on top of
// onto and only its author can regenerate them, so the candidate is refused before its
// gate runs on a tree that could never merge.
//
// What auto-resolution settled is recorded on f, for its verdict to name.
func (r *validation) candidate(ctx context.Context, f *flight) (types.Candidate, error) {
	c := f.change
	if err := fetchHead(ctx, r.vcs, r.clone, c); err != nil {
		return types.Candidate{}, fmt.Errorf("fetch %s: %w", c.Label(), err)
	}
	s := candidateSpec{clone: r.clone, facts: r.facts, onto: f.onto, change: c, scratch: r.scratch, date: r.plan.CommitDate}
	b, err := buildMerge(ctx, r.vcs, s)
	if err == nil && len(b.resolved) > 0 {
		f.resolved = resolvedNote(b.resolved)
		r.Events.Emit(Event{Kind: EventResolved, Change: c.ID, Commit: b.Commit, Reason: f.resolved})
	}
	if err != nil || r.Regenerate == nil {
		return b.Candidate, err
	}
	if b.Commit, err = r.regenerate(ctx, s, b); err != nil {
		r.discard(ctx, b.Candidate)
		return types.Candidate{}, err
	}
	return b.Candidate, nil
}

func (r *validation) regenerate(ctx context.Context, s candidateSpec, b built) (string, error) {
	regen, err := outputs(ctx, s.facts, b.touched)
	if err != nil {
		return "", err
	}
	units, err := r.units(s.change)
	if err != nil {
		return "", err
	}
	keep, err := regenerateWrites(ctx, r.vcs, s, b, r.Regenerate, regen, units)
	if err != nil || len(keep) == 0 {
		return b.Commit, err
	}
	g, err := generationOf(ctx, r.vcs, r.facts, r.clone.Root, r.plan.BaseCommit, s.change, regen)
	if err != nil {
		return "", err
	}
	if !regenerationProven(g) {
		why, code := unprovenWhy(g, regen)
		return "", &types.RefusedError{Paths: keep,
			Reason: joinPaths(keep) + " are stale on top of " + short(s.onto) + ", and " + why + " (" + joinPaths(code) +
				"), so the queue cannot regenerate them for it",
			Remedy: "Merge `" + r.plan.Base + "` in, regenerate, commit what it writes, push, and queue it again."}
	}
	return commitRegenerated(ctx, r.vcs, b, keep)
}

func (r *validation) acquire(ctx context.Context) error {
	select {
	case r.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *validation) release() { <-r.slots }

// start runs f's gate in the background; the slot acquired for f is released when the
// gate returns.
func (r *validation) start(ctx context.Context, group int, f *flight) {
	fctx, cancel := context.WithCancel(ctx)
	f.cancel = cancel
	r.Events.Emit(Event{Kind: EventGate, Change: f.change.ID, Partition: partitionOf(group), Commit: f.cand.Commit, Depth: f.depth})
	go func() {
		defer r.release()
		start := time.Now()
		res, err := r.validate(fctx, f.cand, f.change)
		f.done <- outcome{green: res.Green, summary: res.Summary, err: err, took: time.Since(start)}
	}()
}

// validate gates c's units in cand. A change that reaches no unit has nothing to gate.
func (r *validation) validate(ctx context.Context, cand types.Candidate, c types.Change) (types.GateResult, error) {
	units, err := r.units(c)
	if err != nil || len(units) == 0 {
		return types.GateResult{Green: err == nil}, err
	}
	return r.gate.Validate(ctx, cand, units)
}

// baseRed reports whether the gate is red on onto itself for c: a red there is not c's to
// fix. Each onto and set of units is gated once a run, however many changes ask.
func (r *validation) baseRed(ctx context.Context, onto string, c types.Change) (bool, error) {
	units, err := r.units(c)
	if err != nil {
		return false, err
	}
	key := onto + "\x00" + strings.Join(units, "\x00")
	r.mu.Lock()
	b, ok := r.bases[key]
	if !ok {
		b = &baseGate{}
		r.bases[key] = b
	}
	r.mu.Unlock()
	b.once.Do(func() {
		if b.err = r.acquire(ctx); b.err != nil {
			return
		}
		defer r.release()
		r.Events.Emit(Event{Kind: EventNotice, Change: c.ID, Commit: onto,
			Reason: "gating " + short(onto) + " without #" + c.ID + " to tell whether its red is its own"})
		cand, err := checkout(ctx, r.vcs, r.clone.Root, r.scratch, "base", onto)
		if err != nil {
			b.err = fmt.Errorf("check out %s: %w", short(onto), err)
			return
		}
		defer r.discard(ctx, cand)
		res, err := r.gate.Validate(ctx, cand, units)
		b.red, b.err = !res.Green, err
	})
	return b.red, b.err
}

// verdict decides a gated or refused change. attributable says everything beneath the
// candidate is validated, so a red is the change's own unless the gate is red on what the
// candidate was built onto as well.
func (r *validation) verdict(ctx context.Context, f *flight, out outcome, attributable bool) (types.Decision, error) {
	v := types.Verdict{Change: f.change, After: f.after, Onto: f.onto, CandidateCommit: f.cand.Commit, Method: f.change.Method,
		Depth: f.depth, DurationMS: out.took.Milliseconds()}
	if !out.green {
		what := "the gate failed"
		v.Code = types.CodeKickRed
		if out.refused != nil {
			what, v.Code, v.Paths = "building its candidate failed", types.CodeKickRefused, out.refused.Paths
		}
		if !attributable {
			v.Decision, v.Code, v.Paths = types.DecisionWait, types.CodeWaitBehind, nil
			v.Reason = what + " on top of #" + f.after + ", which this run did not validate; retried once it is"
			return v.Decision, r.decide(v)
		}
		if out.refused == nil {
			red, err := r.baseRed(ctx, f.onto, f.change)
			if err != nil {
				return "", fmt.Errorf("gate %s without %s: %w", short(f.onto), f.change.Label(), err)
			}
			if red {
				v.Decision, v.Code = types.DecisionWait, types.CodeWaitBaseRed
				v.Reason = "the gate failed on it and on " + ontoName(r.plan.Base, f.onto, f.after) + " without it; retried once that is green"
				return v.Decision, r.decide(v)
			}
		}
		v.Decision = types.DecisionKick
		v.Reason = what + ": " + out.summary
		remedy := ""
		if out.refused != nil {
			remedy = out.refused.Remedy
		} else if out.summary != "" {
			v.Reason = out.summary
		}
		v.Report = failureReport(r.plan.Base, f.change.Head, f.onto, f.after, v.Reason, remedy)
		if f.resolved != "" {
			v.Report += "\nBuilding the candidate " + f.resolved + "; the gate ran on that merge.\n"
		}
		return v.Decision, r.decide(v)
	}
	v.Decision, v.Reason = types.DecisionMerge, f.resolved
	return v.Decision, r.decide(v)
}

// ground stops a flight's gate, waits for it, and removes its checkout.
func (r *validation) ground(ctx context.Context, f *flight) {
	f.cancel()
	<-f.done
	r.discard(ctx, f.cand)
}

func (r *validation) discard(ctx context.Context, cand types.Candidate) {
	if err := discard(ctx, r.vcs, r.clone.Root, cand); err != nil {
		r.Events.Emit(Event{Kind: EventNotice, Reason: "remove checkout " + cand.Dir + ": " + err.Error()})
	}
}

// decide records v. A verdict the Applier would refuse to read is refused here, where
// the step that wrote it can say so.
func (r *validation) decide(v types.Verdict) error {
	v.BaseCommit, v.Gate, v.Regenerate = r.plan.BaseCommit, r.Reproduce.Gate, r.Reproduce.Regenerate
	if err := v.Check(); err != nil {
		return err
	}
	r.Events.Emit(Event{Kind: EventDecided, Change: v.Change.ID, Decision: v.Decision, Code: v.Code, Reason: v.Reason,
		Commit: v.CandidateCommit, Depth: v.Depth, DurationMS: v.DurationMS})
	if err := r.dir.Record(v); err != nil {
		return fmt.Errorf("record the verdict on %s: %w", v.Change.Label(), err)
	}
	return nil
}

func conflictAhead(after string, conf sourceConflict) string {
	with := "the commit it was merged onto"
	if after != "" {
		with = "#" + after + " ahead of it"
	}
	reason := "conflicts with " + with + " in " + joinPaths(conf.paths) + "; retried once it merges"
	if note := declinedNote(conf.declined); note != "" {
		reason += "; " + note
	}
	return reason
}

// failureReport says what failed on which commits. How to run it again, the files at
// issue and how to queue the change again are the provider's to render from the kick.
func failureReport(base, head, onto, after, failed, remedy string) string {
	report := fmt.Sprintf("The merge queue built this change at `%s` onto %s, and %s.\n", short(head), ontoName(base, onto, after), failed)
	if remedy != "" {
		report += "\n" + remedy + "\n"
	}
	return report
}

// ontoName names the commit a candidate was built onto: the base's, or the candidate of
// the change validated beneath it.
func ontoName(base, onto, after string) string {
	if after != "" {
		return "the candidate of #" + after + " (`" + short(onto) + "`)"
	}
	return "`" + base + "` at `" + short(onto) + "`"
}

// joinPaths names paths in a report, each a [types.CodeSpan]: a file name is the
// author's to choose, and a report is Markdown.
func joinPaths(paths []string) string {
	shown := make([]string, 0, min(len(paths), 5))
	for _, p := range paths[:min(len(paths), 5)] {
		shown = append(shown, types.CodeSpan(p))
	}
	if len(paths) > 5 {
		return fmt.Sprintf("%s and %d more", strings.Join(shown, ", "), len(paths)-5)
	}
	return strings.Join(shown, ", ")
}
