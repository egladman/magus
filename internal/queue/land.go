package queue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Outcome is what landing did with a change.
type Outcome string

const (
	OutcomeMerged  Outcome = "merged"
	OutcomeKicked  Outcome = "kicked-back"
	OutcomeWaiting Outcome = "waiting"
)

// Result is one change's landing outcome.
type Result struct {
	Change  Change
	Outcome Outcome
	Reason  string
	Commit  string // what the provider merged, for OutcomeMerged
}

// Landing is the write half of a queue run. It trusts the manifest's verdicts, which only
// code approved at its exact head could have produced, but re-checks everything it can
// without running that code: approval at the head, that the head has not moved, that the
// predicted tree differs from the approved change only in derived files, and afterwards
// that the base branch carries the predicted tree.
type Landing struct {
	Provider Provider
	Lander   Lander
	// DryRun reports what would land and calls nothing on the provider.
	DryRun bool
	Log    io.Writer
}

type landing struct {
	*Landing
	results []Result
}

// Run lands m's changes in queue order, each as its own commit. It returns the results
// so far alongside any error; an error means landing stopped, and what it had not
// reached stays queued for the next run.
func (l *Landing) Run(ctx context.Context, m Manifest) ([]Result, error) {
	if l.Provider == nil || l.Lander == nil {
		return nil, errors.New("queue: landing needs a Provider and a Lander")
	}
	r := &landing{Landing: l}
	landed := map[string]bool{}
	for _, p := range m.sorted() {
		c := p.Change
		switch p.Decision {
		case DecisionKick:
			if err := r.kick(ctx, c, c.Head, p.Report); err != nil {
				return r.results, err
			}
		case DecisionWait:
			if err := r.wait(ctx, c, c.Head, p.Reason); err != nil {
				return r.results, err
			}
		case DecisionLand:
			if p.After != "" && !landed[p.After] {
				if err := r.wait(ctx, c, c.Head, "validated on top of #"+p.After+", which did not land"); err != nil {
					return r.results, err
				}
				continue
			}
			ok, err := r.land(ctx, m, p)
			if err != nil {
				return r.results, err
			}
			landed[c.ID] = ok
		default:
			return r.results, fmt.Errorf("queue: manifest decides %q for %s", p.Decision, c.Label())
		}
	}
	return r.results, nil
}

func (r *landing) land(ctx context.Context, m Manifest, p Planned) (bool, error) {
	c := p.Change
	if r.DryRun {
		r.result(c, OutcomeMerged, "would land stage "+short(p.Stage), "")
		return true, nil
	}
	if err := r.Lander.Fetch(ctx, c); err != nil {
		return false, fmt.Errorf("queue: fetch %s: %w", c.Label(), err)
	}
	appr, err := r.Provider.ApprovalAt(ctx, c, c.Head)
	if err != nil {
		return false, fmt.Errorf("queue: approval of %s: %w", c.Label(), err)
	}
	if appr.Head != "" && appr.Head != c.Head {
		r.result(c, OutcomeWaiting, "head moved to "+short(appr.Head)+" after validation", "")
		return false, nil
	}
	if !appr.Approved {
		return false, r.wait(ctx, c, c.Head, "approval at "+short(c.Head)+" was withdrawn"+reasonSuffix(appr.Reason))
	}
	now, err := r.Lander.Tip(ctx, m.Base)
	if err != nil {
		return false, fmt.Errorf("queue: resolve %s: %w", m.Base, err)
	}
	tree, err := r.Lander.Expect(ctx, m.BaseSHA, now, p.Stage)
	if conf, ok := asConflict(err); ok {
		return false, r.wait(ctx, c, c.Head, "conflicts in "+joinPaths(conf.Paths)+" with what landed since validation")
	}
	if err != nil {
		return false, fmt.Errorf("queue: predict %s after %s: %w", m.Base, c.Label(), err)
	}
	prep, err := r.Lander.Prepare(ctx, now, c, tree)
	if refused := (*RefusedError)(nil); errors.As(err, &refused) {
		return false, r.kick(ctx, c, c.Head, fmt.Sprintf("The merge queue validated this change at `%s` but cannot land it: %s\n", short(c.Head), refused.Reason))
	}
	if err != nil {
		return false, fmt.Errorf("queue: prepare %s: %w", c.Label(), err)
	}
	if err := r.post(ctx, c, prep.Merge, StateSuccess, "validated at "+short(p.Stage)); err != nil {
		return false, err
	}
	if err := r.Provider.Merge(ctx, c, prep.Merge, p.Message); err != nil {
		return false, r.wait(ctx, c, prep.Merge, "the host refused the merge: "+err.Error())
	}
	after, err := r.Lander.Tip(ctx, m.Base)
	if err != nil {
		return false, fmt.Errorf("queue: resolve %s after merging %s: %w", m.Base, c.Label(), err)
	}
	got, err := r.Lander.TreeOf(ctx, after)
	if err != nil {
		return false, err
	}
	r.result(c, OutcomeMerged, "", prep.Merge)
	if got != tree {
		// Stop rather than land more on a base nobody validated: something wrote to the
		// branch besides the queue, or the host merged differently than git does.
		return true, fmt.Errorf("queue: %s merged, but %s at %s carries tree %s, not the validated %s; stopping",
			c.Label(), m.Base, short(after), short(got), short(tree))
	}
	r.logf("queue: merged %s at %s", c.Label(), short(prep.Merge))
	return true, nil
}

func (r *landing) kick(ctx context.Context, c Change, sha, report string) error {
	r.result(c, OutcomeKicked, firstLine(report), "")
	if r.DryRun {
		return nil
	}
	if err := r.post(ctx, c, sha, StateFailure, "kicked back; see the comment"); err != nil {
		return err
	}
	if err := r.Provider.KickBack(ctx, c, sha, report); err != nil {
		return fmt.Errorf("queue: kick back %s: %w", c.Label(), err)
	}
	r.logf("queue: kicked back %s", c.Label())
	return nil
}

func (r *landing) wait(ctx context.Context, c Change, sha, reason string) error {
	r.result(c, OutcomeWaiting, reason, "")
	r.logf("queue: %s waits: %s", c.Label(), reason)
	if r.DryRun {
		return nil
	}
	return r.post(ctx, c, sha, StatePending, reason)
}

func (r *landing) post(ctx context.Context, c Change, sha string, state State, desc string) error {
	if err := r.Provider.PostStatus(ctx, c, sha, Status{State: state, Description: desc}); err != nil {
		return fmt.Errorf("queue: post %s on %s: %w", StatusContext, short(sha), err)
	}
	return nil
}

func (r *landing) result(c Change, o Outcome, reason, commit string) {
	res := Result{Change: c, Outcome: o, Reason: reason, Commit: commit}
	if i := slices.IndexFunc(r.results, func(x Result) bool { return x.Change.ID == c.ID }); i >= 0 {
		r.results[i] = res
		return
	}
	r.results = append(r.results, res)
}

func (r *landing) logf(format string, args ...any) {
	if r.Log != nil {
		fmt.Fprintf(r.Log, format+"\n", args...)
	}
}

func joinPaths(paths []string) string {
	if len(paths) > 5 {
		return fmt.Sprintf("%s and %d more", strings.Join(paths[:5], ", "), len(paths)-5)
	}
	return strings.Join(paths, ", ")
}
