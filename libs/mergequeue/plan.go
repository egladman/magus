package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// AffectedFunc answers what a change can affect from the paths it changes, as the
// affected command hook does: the units it reaches, or why that set is not a proof.
type AffectedFunc func(ctx context.Context, c Change, paths []string) (affected []string, unbounded string, err error)

// Planner admits changes and partitions them. It executes no change's code and writes
// nothing to the forge: it calls only [Provider.List] (through the caller) and
// [Provider.ApprovalAt].
type Planner struct {
	// Provider checks approval at each head. Nil admits every change unchecked, for a
	// caller that vouched for its input; landing re-checks approval regardless.
	Provider Provider
	Stager   Stager
	// Affected is asked about a change whose input carries no affected set. Nil leaves
	// such a change unbounded.
	Affected AffectedFunc
	// Depth is how many stages of one partition validate at once. Values below 1 mean 1.
	Depth int
	// Parallel is how many Affected calls run at once. Values below 1 mean 1.
	Parallel int
	Events   *Events
}

// Run plans in. On error the plan holds what was decided before planning stopped.
func (p *Planner) Run(ctx context.Context, in Changes) (Plan, error) {
	if p.Stager == nil {
		return Plan{}, fmt.Errorf("mergequeue: planning needs a Stager")
	}
	plan := Plan{Schema: SchemaPlan, Base: in.Base, Remote: in.Remote, Depth: max(1, p.Depth)}
	tip, err := p.Stager.Tip(ctx, in.Base)
	if err != nil {
		return plan, fmt.Errorf("mergequeue: resolve %s: %w", in.Base, err)
	}
	plan.BaseSHA = tip
	if len(in.Changes) == 0 {
		p.Events.Emit(Event{Event: EventNotice, Reason: "no change carries merge intent against " + in.Base})
	}
	var admitted []Change
	var paths [][]string
	for _, c := range in.Changes {
		changed, ok, err := p.admit(ctx, &plan, tip, &c)
		if err != nil {
			return plan, err
		}
		if ok {
			admitted = append(admitted, c)
			paths = append(paths, changed)
		}
	}
	if err := p.askAffected(ctx, admitted, paths); err != nil {
		return plan, err
	}
	plan.Partitions = Partition(admitted)
	if plan.Partitions == nil {
		plan.Partitions = [][]Change{} // "partitions": [] rather than null for a reader iterating it
	}
	for gi, g := range plan.Partitions {
		ids := make([]string, len(g))
		for i, c := range g {
			ids[i] = c.ID
		}
		p.Events.Emit(Event{Event: EventPartition, Partition: partitionOf(gi), Changes: ids})
	}
	return plan, nil
}

// admit decides whether c enters validation, returning the paths it changes when it does.
func (p *Planner) admit(ctx context.Context, plan *Plan, tip string, c *Change) ([]string, bool, error) {
	decide := func(d Decision, reason, report string) {
		plan.Decided = append(plan.Decided, Decided{Change: *c, Decision: d, Reason: reason, Report: report})
		p.Events.Emit(Event{Event: EventDecided, Change: c.ID, Decision: d, Reason: reason})
	}
	if c.Fork {
		decide(DecisionKick, "a change from a fork", forkReport)
		return nil, false, nil
	}
	if p.Provider != nil {
		appr, err := p.Provider.ApprovalAt(ctx, *c, c.Head)
		if err != nil {
			return nil, false, fmt.Errorf("mergequeue: approval of %s: %w", c.Label(), err)
		}
		if appr.Head != "" && appr.Head != c.Head {
			c.Head = appr.Head
			decide(DecisionWait, "head moved to "+short(appr.Head)+" while listing; retried next run", "")
			return nil, false, nil
		}
		if !appr.Approved {
			decide(DecisionWait, "not approved at "+short(c.Head)+reasonSuffix(appr.Reason), "")
			return nil, false, nil
		}
	}
	if err := p.Stager.Fetch(ctx, *c); err != nil {
		return nil, false, fmt.Errorf("mergequeue: fetch %s: %w", c.Label(), err)
	}
	paths, err := p.Stager.Changed(ctx, tip, c.Head)
	if err != nil {
		return nil, false, fmt.Errorf("mergequeue: changed files of %s: %w", c.Label(), err)
	}
	if err := p.Stager.Overlap(ctx, tip, *c); err != nil {
		conf, ok := asConflict(err)
		if !ok {
			return nil, false, fmt.Errorf("mergequeue: merge %s onto %s: %w", c.Label(), plan.Base, err)
		}
		report := conflictReport(plan.Base, c.Head, conf)
		decide(DecisionKick, firstLine(report), report)
		return nil, false, nil
	}
	return paths, true, nil
}

// askAffected fills in the affected set of every admitted change that arrived without
// one. The hook is a build tool loading its workspace once per call, the slowest step of
// a plan, so the calls run side by side.
func (p *Planner) askAffected(ctx context.Context, admitted []Change, paths [][]string) error {
	if p.Affected == nil {
		return nil
	}
	sem := make(chan struct{}, max(1, p.Parallel))
	errs := make([]error, len(admitted))
	var wg sync.WaitGroup
	for i := range admitted {
		c := &admitted[i]
		if c.Affected != nil || c.Unbounded != "" {
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			affected, unbounded, err := p.Affected(ctx, *c, paths[i])
			if err != nil {
				errs[i] = fmt.Errorf("mergequeue: affected set of %s: %w", c.Label(), err)
				return
			}
			c.Affected, c.Unbounded = affected, unbounded
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

const forkReport = "The merge queue does not land changes from forks: it cannot push their " +
	"regenerated files, and it runs only code whose author can push to this repository. " +
	"Ask a maintainer to push the branch here.\n"

func conflictReport(base, sha string, conf Conflict) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The merge queue could not merge this change at `%s`: it conflicts with `%s` in files that are not derived.\n\n", short(sha), base)
	b.WriteString("Conflicting files:\n")
	for _, p := range conf.Paths {
		fmt.Fprintf(&b, "- `%s`\n", p)
	}
	if len(conf.With) > 0 {
		fmt.Fprintf(&b, "\nCommits on `%s` that changed them:\n", base)
		for _, w := range conf.With {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	fmt.Fprintf(&b, "\nMerge `%s` into this branch, resolve these by hand, push, and queue the change again.\n", base)
	return b.String()
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return ": " + reason
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
