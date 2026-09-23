package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"time"
)

// Validator is the read-only step of a queue run. It executes the changes' code (the
// gate and the regeneration run on staging commits) and so must never hold a
// credential that can write. It calls no [Provider] at all.
type Validator struct {
	// Only, when set, validates that one change: the changes beneath it in its
	// partition are staged under it but not gated, since their own runs gate them.
	// This is how a CI system spreads one plan's stages over separate jobs.
	Only string
	// Parallel caps the stages being built or gated at once across every partition, so
	// many disjoint partitions do not each start Depth builds. Zero means
	// runtime.NumCPU().
	Parallel int
	Events   *Events

	repo StagingRepo
	gate Gate
	sink VerdictSink
}

// NewValidator stages with repo, gates with gate, and hands each verdict to sink the
// moment it is decided, so a Lander can start on it while later stages still run.
func NewValidator(repo StagingRepo, gate Gate, sink VerdictSink) *Validator {
	return &Validator{repo: repo, gate: gate, sink: sink}
}

// Run validates plan's admitted changes, or only v.Only. Partitions run side by side,
// and an error in one stops that partition alone: the verdicts the others reach are
// still sound, and what no verdict reached waits for the next run.
func (v *Validator) Run(ctx context.Context, plan Plan) error {
	if v.repo == nil || v.gate == nil || v.sink == nil {
		return errors.New("a Validator needs a StagingRepo, a Gate and a VerdictSink; build it with NewValidator")
	}
	if v.Parallel < 0 {
		return fmt.Errorf("parallel %d must not be negative", v.Parallel)
	}
	if err := plan.check(); err != nil {
		return err
	}
	n := v.Parallel
	if n == 0 {
		n = runtime.NumCPU()
	}
	r := &validation{Validator: v, plan: plan, slots: make(chan struct{}, n)}
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
	plan  Plan
	slots chan struct{} // one per stage being built or gated
}

type flight struct {
	change Change
	stage  Stage // zero when staging was refused
	onto   string
	after  string // change beneath it, "" at the bottom
	depth  int
	cancel context.CancelFunc
	done   chan outcome
}

type outcome struct {
	green   bool
	summary string
	refused bool // staging refused the change (its regeneration failed) rather than a red gate
	err     error
	took    time.Duration
}

// pipeline runs one partition's speculative stages. Up to Depth stages are in flight,
// each built onto the one below; their gates run concurrently and are consumed in order.
// When the lowest is green its change is decided and the window slides up; when it is
// red its change is the culprit (everything beneath it validated), so it is kicked back
// and every stage above, all built onto it, is rebuilt onto what did validate. A
// refused staging is red the same way, and is attributed only once it is lowest.
func (r *validation) pipeline(ctx context.Context, group int, pending []Change) error {
	onto, ontoID := r.plan.BaseCommit, ""
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
			f := &flight{change: c, onto: onto, after: ontoID, depth: len(inflight) + 1, done: make(chan outcome, 1)}
			for _, below := range slices.Backward(inflight) {
				if below.stage.Commit != "" { // a refused staging is nothing to build onto
					f.onto, f.after = below.stage.Commit, below.change.ID
					break
				}
			}
			queued, err := r.launch(ctx, group, f)
			if err != nil {
				return err
			}
			if queued {
				inflight = append(inflight, f)
			}
		}
		if len(inflight) == 0 {
			return nil
		}
		head := inflight[0]
		inflight = inflight[1:]
		out := <-head.done
		head.cancel()
		r.discard(ctx, head.stage)
		if out.err != nil {
			return fmt.Errorf("gate %s: %w", head.change.Label(), out.err)
		}
		green, err := r.verdict(ctx, head, out, true)
		if err != nil {
			return err
		}
		if !green && head.stage.Commit == "" {
			continue // nothing above was built onto a refused staging
		}
		if !green {
			requeue := make([]Change, 0, len(inflight)+len(pending))
			for _, f := range inflight {
				r.ground(ctx, f)
				requeue = append(requeue, f.change)
			}
			inflight = nil
			pending = append(requeue, pending...)
			continue
		}
		onto, ontoID = head.stage.Commit, head.change.ID
	}
}

// launch stages f and starts its gate, reporting whether f joined the flights. A
// conflict or a wait is decided on the spot and leaves the window; a refused staging
// joins with its red outcome already in hand.
func (r *validation) launch(ctx context.Context, group int, f *flight) (bool, error) {
	if err := r.acquire(ctx); err != nil {
		return false, err
	}
	st, err := r.stage(ctx, f.onto, f.change)
	if err == nil {
		f.stage = st
		r.start(ctx, group, f)
		return true, nil
	}
	r.release()
	var refused *RefusedError
	if errors.As(err, &refused) {
		f.cancel = func() {}
		f.done <- outcome{refused: true, summary: refused.Reason}
		return true, nil
	}
	return false, r.hold(ctx, f, err)
}

// hold decides a wait for a staging error that says nothing against the change, and
// returns the error as it stands when it is the machine's.
func (r *validation) hold(ctx context.Context, f *flight, err error) error {
	var wait *WaitError
	switch conf, ok := asConflict(err); {
	case ok:
		// Planning proved it merges onto the base alone, so it conflicts with a change
		// ahead of it. The next run sees that change landed.
		return r.decide(ctx, Verdict{Change: f.change, Decision: DecisionWait, Reason: conflictAhead(f.after, conf)})
	case errors.As(err, &wait):
		return r.decide(ctx, Verdict{Change: f.change, Decision: DecisionWait, Reason: wait.Reason})
	}
	return fmt.Errorf("stage %s onto %s: %w", f.change.Label(), short(f.onto), err)
}

// only builds v.Only's chain one stage at a time and gates just the top. What is red
// on top of changes this run did not gate cannot be pinned on the top change, so it
// waits for a run where they are validated rather than kicking its author back.
func (r *validation) only(ctx context.Context) error {
	gi, pos, ok := r.plan.Find(r.Only)
	if !ok {
		return fmt.Errorf("%s is not an admitted change of the plan", r.Only)
	}
	onto, after := r.plan.BaseCommit, ""
	var built []Stage
	defer func() {
		for _, s := range built {
			r.discard(ctx, s)
		}
	}()
	for i, c := range r.plan.Partitions[gi][:pos+1] {
		f := &flight{change: c, onto: onto, after: after, depth: len(built) + 1, done: make(chan outcome, 1)}
		st, err := r.stage(ctx, onto, c)
		if err != nil {
			if i < pos {
				continue // its own run decides it; the chain skips it, as the pipeline does
			}
			var refused *RefusedError
			if errors.As(err, &refused) {
				_, err := r.verdict(ctx, f, outcome{refused: true, summary: refused.Reason}, len(built) == 0)
				return err
			}
			return r.hold(ctx, f, err)
		}
		built = append(built, st)
		if i < pos {
			onto, after = st.Commit, c.ID
			continue
		}
		f.stage = st
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

func (r *validation) stage(ctx context.Context, onto string, c Change) (Stage, error) {
	if err := r.repo.FetchHead(ctx, c); err != nil {
		return Stage{}, fmt.Errorf("fetch %s: %w", c.Label(), err)
	}
	return r.repo.Stage(ctx, r.plan.BaseCommit, onto, c)
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
	r.Events.Emit(Event{Kind: EventGate, Change: f.change.ID, Partition: partitionOf(group), Commit: f.stage.Commit, Depth: f.depth})
	go func() {
		defer r.release()
		start := time.Now()
		res, err := r.gate.Validate(fctx, f.stage, f.onto, f.change)
		f.done <- outcome{green: res.Green, summary: res.Summary, err: err, took: time.Since(start)}
	}()
}

// verdict decides a gated or refused change and reports whether it was green.
// attributable says everything beneath the stage is validated, so a red is the
// change's own.
func (r *validation) verdict(ctx context.Context, f *flight, out outcome, attributable bool) (bool, error) {
	v := Verdict{Change: f.change, After: f.after, Onto: f.onto, Stage: f.stage.Commit, Depth: f.depth, DurationMS: out.took.Milliseconds()}
	if !out.green {
		what := "the gate failed"
		if out.refused {
			what = "staging it failed"
		}
		if !attributable {
			v.Decision = DecisionWait
			v.Reason = what + " on top of #" + f.after + ", which this run did not validate; retried once it is"
			return false, r.decide(ctx, v)
		}
		v.Decision = DecisionKick
		v.Reason = what + ": " + out.summary
		v.Report = failureReport(r.plan.Base, f.change.Head, what, out.summary)
		return false, r.decide(ctx, v)
	}
	msg, err := r.repo.SquashMessage(ctx, r.plan.BaseCommit, f.change.Head)
	if err != nil {
		return false, fmt.Errorf("squash message of %s: %w", f.change.Label(), err)
	}
	v.Decision, v.Message = DecisionLand, msg
	return true, r.decide(ctx, v)
}

// ground stops a flight's gate, waits for it, and removes its stage.
func (r *validation) ground(ctx context.Context, f *flight) {
	f.cancel()
	<-f.done
	r.discard(ctx, f.stage)
}

func (r *validation) discard(ctx context.Context, s Stage) {
	if s.Dir == "" {
		return
	}
	if err := r.repo.Discard(context.WithoutCancel(ctx), s); err != nil {
		r.Events.Emit(Event{Kind: EventNotice, Reason: "remove stage " + s.Dir + ": " + err.Error()})
	}
}

func (r *validation) decide(ctx context.Context, v Verdict) error {
	v.BaseCommit = r.plan.BaseCommit
	r.Events.Emit(Event{Kind: EventDecided, Change: v.Change.ID, Decision: v.Decision, Reason: v.Reason,
		Commit: v.Stage, Depth: v.Depth, DurationMS: v.DurationMS})
	if err := r.sink.Record(ctx, v); err != nil {
		return fmt.Errorf("record the verdict on %s: %w", v.Change.Label(), err)
	}
	return nil
}

func conflictAhead(after string, conf Conflict) string {
	with := "the commit it was staged onto"
	if after != "" {
		with = "#" + after + " ahead of it"
	}
	return "conflicts with " + with + " in " + joinPaths(conf.Paths) + "; retried once it lands"
}

func failureReport(base, head, what, summary string) string {
	return fmt.Sprintf("The merge queue validated this change at `%s` on `%s`, and %s.\n\n%s\n\nPush a fix and queue the change again.\n",
		short(head), base, what, summary)
}
