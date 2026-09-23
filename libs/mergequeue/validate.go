package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Validation is the read-only half of a queue run. It executes the changes' code (the
// gate and the regeneration run on staging commits) and so must never hold a
// credential that can write. It calls no [Provider] at all.
type Validation struct {
	Stager Stager
	Gate   Gate
	// Only, when set, validates that one change: the changes beneath it in its
	// partition are staged under it but not gated, since their own runs gate them.
	// This is how a CI system spreads one plan's stages over separate jobs.
	Only string
	// Result receives each change's verdict the moment it is decided, so landing can
	// start on it while later stages still run. Called from one goroutine per
	// partition at once.
	Result func(ctx context.Context, r StageResult) error
	Events *Events
}

// Run validates plan's admitted changes, or only v.Only.
func (v *Validation) Run(ctx context.Context, plan Plan) error {
	if v.Stager == nil || v.Gate == nil || v.Result == nil {
		return errors.New("mergequeue: validation needs a Stager, a Gate and a Result sink")
	}
	if plan.BaseSHA == "" {
		return errors.New("mergequeue: the plan names no base commit")
	}
	r := &validation{Validation: v, plan: plan}
	if v.Only != "" {
		return r.only(ctx)
	}
	// Partitions share no affected unit, so none waits for another.
	var wg sync.WaitGroup
	errs := make([]error, len(plan.Partitions))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for gi, g := range plan.Partitions {
		wg.Go(func() {
			if errs[gi] = r.pipeline(ctx, gi, g); errs[gi] != nil {
				cancel()
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

type validation struct {
	*Validation
	plan Plan
}

func (r *validation) depth() int { return max(1, r.plan.Depth) }

type flight struct {
	change Change
	stage  Stage
	after  string // change beneath it, "" at the bottom
	depth  int
	cancel context.CancelFunc
	done   chan gateOutcome
}

type gateOutcome struct {
	res  GateResult
	err  error
	took time.Duration
}

// pipeline runs one partition's speculative stages. Up to Depth stages are in flight,
// each built on the one below; their gates run concurrently and are consumed in order.
// When the lowest is green its change is decided and the window slides up; when it is
// red its change is the culprit (everything beneath it validated), so it is kicked back
// and every stage above, all built on it, is rebuilt on what did validate.
func (r *validation) pipeline(ctx context.Context, group int, pending []Change) error {
	on, onID := r.plan.BaseSHA, ""
	var inflight []*flight
	defer func() {
		for _, f := range inflight {
			r.ground(ctx, f)
		}
	}()
	for {
		for len(inflight) < r.depth() && len(pending) > 0 {
			c := pending[0]
			pending = pending[1:]
			below, after := on, onID
			if n := len(inflight); n > 0 {
				below, after = inflight[n-1].stage.Commit, inflight[n-1].change.ID
			}
			st, err := r.Stager.Build(ctx, r.plan.BaseSHA, below, c)
			if conf, ok := asConflict(err); ok {
				// Planning proved it merges onto the base alone, so it conflicts with a
				// change ahead of it. The next run sees that change landed.
				if err := r.decide(ctx, StageResult{Change: c, Decision: DecisionWait,
					Reason: "conflicts with #" + after + " ahead of it in " + strings.Join(conf.Paths, ", ") + "; retried once it lands"}); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return fmt.Errorf("mergequeue: stage %s on %s: %w", c.Label(), short(below), err)
			}
			f := &flight{change: c, stage: st, after: after, depth: len(inflight) + 1, done: make(chan gateOutcome, 1)}
			r.start(ctx, group, f, below)
			inflight = append(inflight, f)
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
			return fmt.Errorf("mergequeue: gate %s: %w", head.change.Label(), out.err)
		}
		green, err := r.verdict(ctx, head, out)
		if err != nil {
			return err
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
		on, onID = head.stage.Commit, head.change.ID
	}
}

// only builds v.Only's chain one stage at a time and gates just the top.
func (r *validation) only(ctx context.Context) error {
	gi, pos, ok := r.plan.Find(r.Only)
	if !ok {
		return fmt.Errorf("mergequeue: %s is not an admitted change of the plan", r.Only)
	}
	on, after := r.plan.BaseSHA, ""
	var built []Stage
	defer func() {
		for _, s := range built {
			r.discard(ctx, s)
		}
	}()
	for i, c := range r.plan.Partitions[gi][:pos+1] {
		st, err := r.Stager.Build(ctx, r.plan.BaseSHA, on, c)
		if conf, ok := asConflict(err); ok {
			if i < pos {
				continue // its own run holds it; the chain above skips it, as the pipeline does
			}
			return r.decide(ctx, StageResult{Change: c, Decision: DecisionWait,
				Reason: "conflicts with #" + after + " ahead of it in " + strings.Join(conf.Paths, ", ") + "; retried once it lands"})
		}
		if err != nil {
			return fmt.Errorf("mergequeue: stage %s on %s: %w", c.Label(), short(on), err)
		}
		built = append(built, st)
		if i < pos {
			on, after = st.Commit, c.ID
			continue
		}
		f := &flight{change: c, stage: st, after: after, depth: len(built), done: make(chan gateOutcome, 1)}
		r.start(ctx, gi, f, on)
		out := <-f.done
		f.cancel()
		if out.err != nil {
			return fmt.Errorf("mergequeue: gate %s: %w", c.Label(), out.err)
		}
		_, err = r.verdict(ctx, f, out)
		return err
	}
	return nil
}

func (r *validation) start(ctx context.Context, group int, f *flight, below string) {
	fctx, cancel := context.WithCancel(ctx)
	f.cancel = cancel
	r.Events.Emit(Event{Event: EventGate, Change: f.change.ID, Partition: partitionOf(group), Commit: f.stage.Commit, Depth: f.depth})
	go func() {
		start := time.Now()
		res, err := r.Gate.Validate(fctx, f.stage, below, f.change)
		f.done <- gateOutcome{res: res, err: err, took: time.Since(start)}
	}()
}

// verdict decides a gated change and reports whether it was green.
func (r *validation) verdict(ctx context.Context, f *flight, out gateOutcome) (bool, error) {
	res := StageResult{Change: f.change, After: f.after, Stage: f.stage.Commit, Depth: f.depth, DurationMS: out.took.Milliseconds()}
	if !out.res.Green {
		res.Decision = DecisionKick
		res.Reason = "the gate failed: " + out.res.Summary
		res.Report = failureReport(r.plan.Base, f.change.Head, out.res)
		return false, r.decide(ctx, res)
	}
	msg, err := r.Stager.Message(ctx, r.plan.BaseSHA, f.change.Head)
	if err != nil {
		return false, fmt.Errorf("mergequeue: squash message of %s: %w", f.change.Label(), err)
	}
	res.Decision, res.Message = DecisionLand, msg
	return true, r.decide(ctx, res)
}

// ground stops a flight's gate, waits for it, and removes its stage.
func (r *validation) ground(ctx context.Context, f *flight) {
	f.cancel()
	<-f.done
	r.discard(ctx, f.stage)
}

func (r *validation) discard(ctx context.Context, s Stage) {
	if err := r.Stager.Discard(context.WithoutCancel(ctx), s); err != nil {
		r.Events.Emit(Event{Event: EventNotice, Reason: "remove stage " + s.Dir + ": " + err.Error()})
	}
}

func (r *validation) decide(ctx context.Context, res StageResult) error {
	res.Schema, res.BaseSHA = SchemaStage, r.plan.BaseSHA
	r.Events.Emit(Event{Event: EventDecided, Change: res.Change.ID, Decision: res.Decision, Reason: res.Reason,
		Commit: res.Stage, Depth: res.Depth, DurationMS: res.DurationMS})
	if err := r.Result(ctx, res); err != nil {
		return fmt.Errorf("mergequeue: record the verdict on %s: %w", res.Change.Label(), err)
	}
	return nil
}

func failureReport(base, sha string, res GateResult) string {
	return fmt.Sprintf("The merge queue validated this change at `%s` on `%s`, and the gate failed.\n\n%s\n\nPush a fix and queue the change again.\n",
		short(sha), base, res.Summary)
}
