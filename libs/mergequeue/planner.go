package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Planner admits changes and partitions them. It executes no change's code and writes
// nothing to the forge: it calls only [Provider.ApprovalAt].
type Planner struct {
	// Provider checks approval at each head. Nil admits every change unchecked, for a
	// caller that vouched for its input; an Applier re-checks approval regardless.
	Provider Provider
	// Affected is asked about a change whose input carries no affected set. Nil leaves
	// such a change unbounded.
	Affected AffectedFunc
	// Depth is how many stages of one partition validate at once. Zero means 1.
	Depth int
	// Parallel is how many changes are admitted (fetched, checked, and put to Affected)
	// at once. Zero means 1.
	Parallel int
	Events   *Events

	repo StagingRepo
}

// NewPlanner plans against repo.
func NewPlanner(repo StagingRepo) *Planner { return &Planner{repo: repo} }

// Run plans in. Changes are admitted concurrently, but the plan lists them in queue
// order.
func (p *Planner) Run(ctx context.Context, in Changes) (Plan, error) {
	if p.repo == nil {
		return Plan{}, errors.New("a Planner needs a StagingRepo; build it with NewPlanner")
	}
	if p.Depth < 0 || p.Parallel < 0 {
		return Plan{}, fmt.Errorf("depth %d and parallel %d must not be negative", p.Depth, p.Parallel)
	}
	if err := in.check(); err != nil {
		return Plan{}, err
	}
	tip, err := p.repo.FetchTip(ctx, in.Base)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve %s: %w", in.Base, err)
	}
	plan := Plan{Schema: SchemaPlan, Base: in.Base, BaseCommit: tip, Depth: max(1, p.Depth)}
	if len(in.Changes) == 0 {
		p.Events.Emit(Event{Kind: EventNotice, Reason: "no change carries merge intent against " + in.Base})
	}

	admitted := make([]admission, len(in.Changes))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, max(1, p.Parallel))
	errs := make([]error, len(in.Changes))
	var wg sync.WaitGroup
	for i, c := range in.Changes {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			defer func() { <-sem }()
			if admitted[i], errs[i] = p.admit(ctx, in.Base, tip, c); errs[i] != nil {
				cancel()
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return Plan{}, err
	}

	var changes []Change
	for _, a := range admitted {
		if a.verdict != nil {
			plan.Verdicts = append(plan.Verdicts, *a.verdict)
			p.Events.Emit(Event{Kind: EventDecided, Change: a.verdict.Change.ID, Decision: a.verdict.Decision, Reason: a.verdict.Reason})
			continue
		}
		changes = append(changes, a.change)
	}
	plan.Partitions = Partition(changes)
	for gi, g := range plan.Partitions {
		ids := make([]string, len(g))
		for i, c := range g {
			ids[i] = c.ID
		}
		p.Events.Emit(Event{Kind: EventPartition, Partition: partitionOf(gi), Changes: ids})
	}
	return plan, nil
}

// admission is one change's outcome: a verdict when planning settled it, else the
// change as admitted, its affected set filled in.
type admission struct {
	change  Change
	verdict *Verdict
}

func (p *Planner) admit(ctx context.Context, base, tip string, c Change) (admission, error) {
	decide := func(d Decision, reason, report string) (admission, error) {
		return admission{verdict: &Verdict{Change: c, Decision: d, Reason: reason, Report: report}}, nil
	}
	if c.Fork {
		return decide(DecisionKick, "a change from a fork", forkReport)
	}
	if err := p.repo.FetchHead(ctx, c); err != nil {
		return admission{}, fmt.Errorf("fetch %s: %w", c.Label(), err)
	}
	if p.Provider != nil {
		appr, reviewed, err := approval(ctx, p.Provider, p.repo, tip, c)
		if err != nil {
			return admission{}, err
		}
		if appr.Head != c.Head {
			c.Head = appr.Head
			return decide(DecisionWait, "head moved to "+short(appr.Head)+" while listing; retried next run", "")
		}
		if !appr.Approved {
			return decide(DecisionWait, "not approved at "+short(reviewed)+reasonSuffix(appr.Reason), "")
		}
	}
	paths, err := p.repo.Changed(ctx, tip, c.Head)
	if err != nil {
		return admission{}, fmt.Errorf("changed files of %s: %w", c.Label(), err)
	}
	if err := p.repo.CheckMerge(ctx, tip, c); err != nil {
		conf, ok := asConflict(err)
		if !ok {
			return admission{}, fmt.Errorf("merge %s onto %s: %w", c.Label(), base, err)
		}
		report := conflictReport(base, c.Head, conf)
		return decide(DecisionKick, firstLine(report), report)
	}
	if c.Affected == nil && c.UnboundedBy == "" && p.Affected != nil {
		// The hook is a build tool loading its workspace once per call, the slowest step
		// of a plan, which is why admission runs side by side.
		affected, unboundedBy, err := p.Affected(ctx, c, paths)
		if err != nil {
			return admission{}, fmt.Errorf("affected set of %s: %w", c.Label(), err)
		}
		c.Affected, c.UnboundedBy = affected, unboundedBy
	}
	return admission{change: c}, nil
}

// reviewTargeter is what approval needs of either repo.
type reviewTargeter interface {
	ReviewTarget(ctx context.Context, tip, head string) (string, error)
}

// approval asks prov for c's approval at the commit a review of c.Head covers, and
// returns it with that commit. A provider that reports no head is broken: without it
// the queue cannot tell a moved head from an approved one.
func approval(ctx context.Context, prov Provider, repo reviewTargeter, tip string, c Change) (Approval, string, error) {
	reviewed, err := repo.ReviewTarget(ctx, tip, c.Head)
	if err != nil {
		return Approval{}, "", fmt.Errorf("review target of %s: %w", c.Label(), err)
	}
	appr, err := prov.ApprovalAt(ctx, c, reviewed)
	if err != nil {
		return Approval{}, "", fmt.Errorf("approval of %s: %w", c.Label(), err)
	}
	if appr.Head == "" {
		return Approval{}, "", fmt.Errorf("approval of %s: the provider reported no head", c.Label())
	}
	return appr, reviewed, nil
}

const forkReport = "The merge queue does not merge changes from forks: it cannot push their " +
	"regenerated files, and it runs only code whose author can push to this repository. " +
	"Ask a maintainer to push the branch here.\n"

func conflictReport(base, commit string, conf Conflict) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The merge queue could not merge this change at `%s`: it conflicts with `%s` in files that are not derived.\n\n", short(commit), base)
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

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
