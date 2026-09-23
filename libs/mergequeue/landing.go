package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Results supplies validation's verdicts to landing as they appear.
type Results interface {
	// Poll returns every verdict available now, keyed by change id, and whether no more
	// will arrive. A source must read its end-of-results signal before the verdicts, so
	// a final poll sees everything.
	Poll(ctx context.Context) (map[string]StageResult, bool, error)
}

// Landing is the write half of a queue run. It trusts validation's verdicts, which
// only code approved at its exact head could have produced, but re-checks everything
// it can without running that code: approval at the head, that the head has not moved,
// that the predicted tree differs from the approved change only in derived files, and
// afterwards that the base branch carries the predicted tree.
type Landing struct {
	Provider Provider
	Lander   Lander
	// StatusContext names the commit status landing posts; empty means
	// [DefaultStatusContext].
	StatusContext string
	// Interval is how long to wait between polls while verdicts are outstanding.
	Interval time.Duration
	// DryRun reports what would land and calls nothing on the provider.
	DryRun bool
	Events *Events
}

// Run lands plan's changes as their verdicts arrive, each as its own commit, as soon as
// every change beneath it in its partition has landed. Partitions land independently.
// It returns once every admitted change is settled or the source is exhausted; what it
// did not reach stays queued for the next run. An error means landing stopped.
func (l *Landing) Run(ctx context.Context, plan Plan, src Results) error {
	if l.Provider == nil || l.Lander == nil || src == nil {
		return errors.New("mergequeue: landing needs a Provider, a Lander and a Results source")
	}
	r := &landing{Landing: l, plan: plan, landed: map[string]bool{}}
	for _, d := range plan.Decided {
		var err error
		switch d.Decision {
		case DecisionKick:
			err = r.kick(ctx, d.Change, d.Change.Head, d.Report)
		case DecisionWait:
			err = r.wait(ctx, d.Change, d.Change.Head, d.Reason)
		default:
			err = fmt.Errorf("mergequeue: the plan decides %q for %s", d.Decision, d.Change.Label())
		}
		if err != nil {
			return err
		}
	}
	queues := make([][]Change, len(plan.Partitions))
	copy(queues, plan.Partitions)
	for {
		results, done, err := src.Poll(ctx)
		if err != nil {
			return err
		}
		progress := false
		remaining := 0
		for gi := range queues {
			for len(queues[gi]) > 0 {
				res, ok := results[queues[gi][0].ID]
				if !ok {
					break
				}
				if err := r.settle(ctx, res); err != nil {
					return err
				}
				queues[gi] = queues[gi][1:]
				progress = true
			}
			remaining += len(queues[gi])
		}
		if remaining == 0 {
			return nil
		}
		if done && !progress {
			for _, q := range queues {
				for _, c := range q {
					if err := r.wait(ctx, c, c.Head, "not validated in this run"); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if progress {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(max(l.Interval, 10*time.Millisecond)):
		}
	}
}

type landing struct {
	*Landing
	plan   Plan
	landed map[string]bool
}

func (r *landing) settle(ctx context.Context, res StageResult) error {
	c := res.Change
	switch res.Decision {
	case DecisionKick:
		return r.kick(ctx, c, c.Head, res.Report)
	case DecisionWait:
		return r.wait(ctx, c, c.Head, res.Reason)
	case DecisionLand:
		if res.After != "" && !r.landed[res.After] {
			return r.wait(ctx, c, c.Head, "validated on top of #"+res.After+", which did not land")
		}
		ok, err := r.land(ctx, res)
		r.landed[c.ID] = ok
		return err
	}
	return fmt.Errorf("mergequeue: a verdict decides %q for %s", res.Decision, c.Label())
}

func (r *landing) land(ctx context.Context, res StageResult) (bool, error) {
	c := res.Change
	if res.BaseSHA != r.plan.BaseSHA {
		return false, r.wait(ctx, c, c.Head, "validated on "+short(res.BaseSHA)+", not this plan's base "+short(r.plan.BaseSHA))
	}
	if r.DryRun {
		r.Events.Emit(Event{Event: EventMerged, Change: c.ID, Commit: res.Stage, Reason: "dry run: would land stage " + short(res.Stage)})
		return true, nil
	}
	if res.Bundle != "" {
		if err := r.Lander.Import(ctx, res.Bundle); err != nil {
			return false, fmt.Errorf("mergequeue: import the stage of %s: %w", c.Label(), err)
		}
	}
	if err := r.Lander.Fetch(ctx, c); err != nil {
		return false, fmt.Errorf("mergequeue: fetch %s: %w", c.Label(), err)
	}
	appr, err := r.Provider.ApprovalAt(ctx, c, c.Head)
	if err != nil {
		return false, fmt.Errorf("mergequeue: approval of %s: %w", c.Label(), err)
	}
	if appr.Head != "" && appr.Head != c.Head {
		r.Events.Emit(Event{Event: EventWaiting, Change: c.ID, Reason: "head moved to " + short(appr.Head) + " after validation"})
		return false, nil
	}
	if !appr.Approved {
		return false, r.wait(ctx, c, c.Head, "approval at "+short(c.Head)+" was withdrawn"+reasonSuffix(appr.Reason))
	}
	now, err := r.Lander.Tip(ctx, r.plan.Base)
	if err != nil {
		return false, fmt.Errorf("mergequeue: resolve %s: %w", r.plan.Base, err)
	}
	tree, err := r.Lander.Expect(ctx, r.plan.BaseSHA, now, res.Stage)
	if conf, ok := asConflict(err); ok {
		return false, r.wait(ctx, c, c.Head, "conflicts in "+joinPaths(conf.Paths)+" with what landed since validation")
	}
	if err != nil {
		return false, fmt.Errorf("mergequeue: predict %s after %s: %w", r.plan.Base, c.Label(), err)
	}
	prep, err := r.Lander.Prepare(ctx, r.plan.BaseSHA, now, c, tree)
	if refused := (*RefusedError)(nil); errors.As(err, &refused) {
		return false, r.kick(ctx, c, c.Head, fmt.Sprintf("The merge queue validated this change at `%s` but cannot land it: %s\n", short(c.Head), refused.Reason))
	}
	if err != nil {
		return false, fmt.Errorf("mergequeue: prepare %s: %w", c.Label(), err)
	}
	if err := r.post(ctx, c, prep.Merge, StateSuccess, "validated at "+short(res.Stage)); err != nil {
		return false, err
	}
	if err := r.Provider.Merge(ctx, c, prep.Merge, res.Message); err != nil {
		return false, r.wait(ctx, c, prep.Merge, "the host refused the merge: "+err.Error())
	}
	after, err := r.Lander.Tip(ctx, r.plan.Base)
	if err != nil {
		return true, fmt.Errorf("mergequeue: resolve %s after merging %s: %w", r.plan.Base, c.Label(), err)
	}
	got, err := r.Lander.TreeOf(ctx, after)
	if err != nil {
		return true, err
	}
	r.Events.Emit(Event{Event: EventMerged, Change: c.ID, Commit: prep.Merge})
	if got != tree {
		// Stop rather than land more on a base nobody validated: something wrote to the
		// branch besides the queue, or the host merged differently than git does.
		return true, fmt.Errorf("mergequeue: %s merged, but %s at %s carries tree %s, not the validated %s; stopping",
			c.Label(), r.plan.Base, short(after), short(got), short(tree))
	}
	return true, nil
}

func (r *landing) kick(ctx context.Context, c Change, sha, report string) error {
	r.Events.Emit(Event{Event: EventKicked, Change: c.ID, Reason: firstLine(report)})
	if r.DryRun {
		return nil
	}
	if err := r.post(ctx, c, sha, StateFailure, "kicked back; see the comment"); err != nil {
		return err
	}
	if err := r.Provider.KickBack(ctx, c, sha, report); err != nil {
		return fmt.Errorf("mergequeue: kick back %s: %w", c.Label(), err)
	}
	return nil
}

func (r *landing) wait(ctx context.Context, c Change, sha, reason string) error {
	r.Events.Emit(Event{Event: EventWaiting, Change: c.ID, Reason: reason})
	if r.DryRun {
		return nil
	}
	return r.post(ctx, c, sha, StatePending, reason)
}

func (r *landing) post(ctx context.Context, c Change, sha string, state State, desc string) error {
	name := r.StatusContext
	if name == "" {
		name = DefaultStatusContext
	}
	if err := r.Provider.PostStatus(ctx, c, sha, Status{Context: name, State: state, Description: desc}); err != nil {
		return fmt.Errorf("mergequeue: post %s on %s: %w", name, short(sha), err)
	}
	return nil
}

func joinPaths(paths []string) string {
	if len(paths) > 5 {
		return fmt.Sprintf("%s and %d more", strings.Join(paths[:5], ", "), len(paths)-5)
	}
	return strings.Join(paths, ", ")
}
