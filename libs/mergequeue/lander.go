package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Lander is the write step of a queue run. It trusts validation's verdicts only as far
// as the plan vouches for them, and re-checks everything it can without running any
// change's code: approval at the head, that the head has not moved, that the predicted
// tree differs from the approved change only in derived files, and afterwards that the
// base branch carries the predicted tree.
//
// The status it posts reads success only once a change has merged. A required status
// that goes green before the merge would let anyone merge the change on whatever the
// base branch is by then, so the landing credential must be one branch protection lets
// bypass the queue's own status check.
type Lander struct {
	// StatusContext names the commit status a Lander posts; empty means
	// [DefaultStatusContext].
	StatusContext string
	// Interval is how long to wait between polls while verdicts are outstanding.
	Interval time.Duration
	// DryRun reports what would land and calls nothing on the provider.
	DryRun bool
	Events *Events

	provider Provider
	repo     LandingRepo
	src      VerdictSource
}

// NewLander lands through provider and repo the verdicts src supplies.
func NewLander(provider Provider, repo LandingRepo, src VerdictSource) *Lander {
	return &Lander{provider: provider, repo: repo, src: src}
}

// Run lands plan's changes as their verdicts arrive, each as its own commit, as soon as
// every change beneath it in its partition has landed. Partitions land independently.
// It returns once every admitted change is settled or the source is exhausted; what it
// did not reach stays queued for the next run. An error means landing stopped.
func (l *Lander) Run(ctx context.Context, plan Plan) error {
	if l.provider == nil || l.repo == nil || l.src == nil {
		return errors.New("a Lander needs a Provider, a LandingRepo and a VerdictSource; build it with NewLander")
	}
	if err := plan.check(); err != nil {
		return err
	}
	r := &landing{Lander: l, plan: plan, landed: map[string]bool{}, got: map[string]Verdict{}}
	for _, v := range plan.Verdicts {
		if err := r.settle(ctx, v); err != nil {
			return err
		}
	}
	queues := slices.Clone(plan.Partitions)
	for {
		fresh, done, err := l.src.Poll(ctx)
		if err != nil {
			return err
		}
		for _, v := range fresh {
			if err := r.accept(v); err != nil {
				return err
			}
		}
		progress := false
		remaining := 0
		for gi := range queues {
			for len(queues[gi]) > 0 {
				v, ok := r.got[queues[gi][0].ID]
				if !ok {
					break
				}
				if err := r.settle(ctx, v); err != nil {
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
	*Lander
	plan   Plan
	landed map[string]bool
	got    map[string]Verdict
}

// accept files a verdict under the plan's own record of its change. A verdict names
// its change, but only the plan says which head was admitted and what lies beneath
// it, so a verdict that disagrees with the plan lands nothing.
func (r *landing) accept(v Verdict) error {
	gi, pos, ok := r.plan.Find(v.Change.ID)
	if !ok {
		return fmt.Errorf("a verdict names #%s, which the plan did not admit", v.Change.ID)
	}
	planned := r.plan.Partitions[gi][pos]
	switch {
	case v.Change.Head != planned.Head:
		v = Verdict{Change: planned, Decision: DecisionWait,
			Reason: "validated at " + short(v.Change.Head) + ", not the planned head " + short(planned.Head)}
	case v.Decision == DecisionLand && v.After != "" && !slices.ContainsFunc(r.plan.Partitions[gi][:pos], func(c Change) bool { return c.ID == v.After }):
		v = Verdict{Change: planned, Decision: DecisionWait,
			Reason: "validated on top of #" + v.After + ", which is not beneath it in its partition"}
	case v.Decision == DecisionLand && v.After == "" && v.Onto != r.plan.BaseCommit:
		v = Verdict{Change: planned, Decision: DecisionWait,
			Reason: "validated at the bottom of its partition, but onto " + short(v.Onto) + ", not the plan's base"}
	default:
		v.Change = planned
	}
	r.got[planned.ID] = v
	return nil
}

func (r *landing) settle(ctx context.Context, v Verdict) error {
	c := v.Change
	switch v.Decision {
	case DecisionKick:
		return r.kick(ctx, c, c.Head, v.Report)
	case DecisionWait:
		return r.wait(ctx, c, c.Head, v.Reason)
	case DecisionLand:
		if v.After != "" && !r.landed[v.After] {
			return r.wait(ctx, c, c.Head, "validated on top of #"+v.After+", which did not land")
		}
		if v.After != "" && r.got[v.After].Stage != v.Onto {
			return r.wait(ctx, c, c.Head, "validated onto "+short(v.Onto)+", not the stage #"+v.After+" landed from")
		}
		ok, err := r.land(ctx, v)
		r.landed[c.ID] = ok
		return err
	}
	return fmt.Errorf("a verdict decides %q for %s", v.Decision, c.Label())
}

func (r *landing) land(ctx context.Context, v Verdict) (bool, error) {
	c := v.Change
	if v.BaseCommit != r.plan.BaseCommit {
		return false, r.wait(ctx, c, c.Head, "validated on "+short(v.BaseCommit)+", not this plan's base "+short(r.plan.BaseCommit))
	}
	if r.DryRun {
		r.Events.Emit(Event{Kind: EventMerged, Change: c.ID, Commit: v.Stage, Reason: "dry run: would land stage " + short(v.Stage)})
		return true, nil
	}
	if v.Bundle != "" {
		if err := r.repo.ImportBundle(ctx, v.Bundle); err != nil {
			return false, fmt.Errorf("import the stage of %s: %w", c.Label(), err)
		}
	}
	if err := r.repo.FetchHead(ctx, c); err != nil {
		return false, fmt.Errorf("fetch %s: %w", c.Label(), err)
	}
	tip, err := r.repo.FetchTip(ctx, r.plan.Base)
	if err != nil {
		return false, fmt.Errorf("resolve %s: %w", r.plan.Base, err)
	}
	appr, reviewed, err := approval(ctx, r.provider, r.repo, tip, c)
	if err != nil {
		return false, err
	}
	if appr.Head != c.Head {
		moved := c
		moved.Head = appr.Head
		return false, r.wait(ctx, moved, appr.Head, "head moved to "+short(appr.Head)+" after validation; retried next run")
	}
	if !appr.Approved {
		return false, r.wait(ctx, c, c.Head, "approval at "+short(reviewed)+" was withdrawn"+reasonSuffix(appr.Reason))
	}
	tree, err := r.repo.Predict(ctx, r.plan.BaseCommit, tip, v.Onto, v.Stage)
	if conf, ok := asConflict(err); ok {
		return false, r.wait(ctx, c, c.Head, joinPaths(conf.Paths)+" changed both here and in what landed since validation; restaged next run")
	}
	if err != nil {
		return false, fmt.Errorf("predict %s after %s: %w", r.plan.Base, c.Label(), err)
	}
	commit, err := r.repo.PushLanding(ctx, r.plan.BaseCommit, tip, c, tree)
	var refused *RefusedError
	var held *WaitError
	switch {
	case errors.As(err, &refused):
		return false, r.kick(ctx, c, c.Head, fmt.Sprintf("The merge queue validated this change at `%s` but cannot land it: %s\n", short(c.Head), refused.Reason))
	case errors.As(err, &held):
		return false, r.wait(ctx, c, c.Head, held.Reason)
	case err != nil:
		return false, fmt.Errorf("push the landing commit of %s: %w", c.Label(), err)
	}
	if err := r.post(ctx, c, commit, StatePending, "landing stage "+short(v.Stage)); err != nil {
		return false, err
	}
	if err := r.provider.MergeChange(ctx, c, commit, v.Message); err != nil {
		return false, r.wait(ctx, c, commit, "the host refused the merge: "+err.Error())
	}
	after, err := r.repo.FetchTip(ctx, r.plan.Base)
	if err != nil {
		return true, fmt.Errorf("resolve %s after merging %s: %w", r.plan.Base, c.Label(), err)
	}
	got, err := r.repo.TreeOf(ctx, after)
	if err != nil {
		return true, err
	}
	r.Events.Emit(Event{Kind: EventMerged, Change: c.ID, Commit: commit})
	if got != tree {
		// Stop rather than land more on a base nobody validated: something wrote to the
		// branch besides the queue, or the host merged differently than git does.
		return true, fmt.Errorf("%s merged, but %s at %s carries tree %s, not the validated %s; stopping",
			c.Label(), r.plan.Base, short(after), short(got), short(tree))
	}
	if err := r.post(ctx, c, commit, StateSuccess, "landed as "+short(after)); err != nil {
		// The merge stands and main carries the validated tree; the status is a record.
		r.Events.Emit(Event{Kind: EventNotice, Change: c.ID, Reason: err.Error()})
	}
	return true, nil
}

func (r *landing) kick(ctx context.Context, c Change, commit, report string) error {
	r.Events.Emit(Event{Kind: EventKicked, Change: c.ID, Reason: firstLine(report)})
	if r.DryRun {
		return nil
	}
	if err := r.post(ctx, c, commit, StateFailure, "kicked back; see the comment"); err != nil {
		return err
	}
	if err := r.provider.KickBack(ctx, c, commit, report); err != nil {
		return fmt.Errorf("kick back %s: %w", c.Label(), err)
	}
	return nil
}

func (r *landing) wait(ctx context.Context, c Change, commit, reason string) error {
	r.Events.Emit(Event{Kind: EventWaiting, Change: c.ID, Reason: reason})
	if r.DryRun {
		return nil
	}
	return r.post(ctx, c, commit, StatePending, reason)
}

func (r *landing) post(ctx context.Context, c Change, commit string, state CommitState, desc string) error {
	name := r.StatusContext
	if name == "" {
		name = DefaultStatusContext
	}
	if err := r.provider.PostStatus(ctx, c, commit, CommitStatus{Context: name, State: state, Description: desc}); err != nil {
		return fmt.Errorf("post %s on %s: %w", name, short(commit), err)
	}
	return nil
}

func joinPaths(paths []string) string {
	if len(paths) > 5 {
		return fmt.Sprintf("%s and %d more", strings.Join(paths[:5], ", "), len(paths)-5)
	}
	return strings.Join(paths, ", ")
}
