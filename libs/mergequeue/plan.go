package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/libs/mergequeue/types"
)

// Planner admits changes and partitions them. It executes no change's code and writes
// nothing to the provider: it calls only [types.Provider.Describe] and
// [types.Provider.ApprovalAt].
type Planner struct {
	// Depth is how many candidates of one partition validate at once. Zero means 1.
	Depth int
	// Parallel is how many changes are admitted (fetched, checked, and put to the build
	// tool) at once. Zero means runtime.NumCPU().
	Parallel int
	Events   *Events

	vcs      types.ReadVCS
	clone    Clone
	provider types.Provider
	facts    types.BuildFacts
}

// NewPlanner plans in cl with v, checking approval with p and asking f what each change
// affects and which files are generated.
func NewPlanner(v types.ReadVCS, cl Clone, p types.Provider, f types.BuildFacts) (*Planner, error) {
	switch {
	case v == nil || p == nil || f == nil:
		return nil, errors.New("planner needs a VCS, a provider and build facts")
	case cl.check() != nil:
		return nil, cl.check()
	}
	return &Planner{vcs: v, clone: cl, provider: p, facts: f}, nil
}

// planning is one Run's state.
type planning struct {
	*Planner
	in    types.Changes
	tip   string
	caps  types.Capabilities
	heads []string // every head a change may be stacked on: listed and merged
}

// Run plans in. Changes are admitted concurrently, but the plan lists them in queue
// order, each after the change it is stacked on.
func (p *Planner) Run(ctx context.Context, in types.Changes) (types.Plan, error) {
	if p.Depth < 0 || p.Parallel < 0 {
		return types.Plan{}, fmt.Errorf("depth %d and parallel %d must not be negative", p.Depth, p.Parallel)
	}
	if err := in.Check(); err != nil {
		return types.Plan{}, err
	}
	tip, err := fetchBase(ctx, p.vcs, p.clone, in.Base)
	if err != nil {
		return types.Plan{}, err
	}
	r := &planning{Planner: p, in: in, tip: tip}
	if r.caps, err = p.provider.Describe(ctx, types.ListQuery{Base: in.Base, RemoteURL: in.RemoteURL}); err != nil {
		return types.Plan{}, fmt.Errorf("describe the provider: %w", err)
	}
	if err := r.caps.Check(); err != nil {
		return types.Plan{}, err
	}
	if err := r.checkMerged(ctx); err != nil {
		return types.Plan{}, err
	}
	plan := types.Plan{Schema: types.SchemaPlan, Base: in.Base, RemoteURL: in.RemoteURL, BaseCommit: tip, Depth: max(1, p.Depth),
		Merged: in.Merged, Unqueued: in.Unqueued}
	if len(in.Changes) == 0 {
		p.Events.Emit(Event{Kind: EventNotice, Reason: "no change carries merge intent against " + in.Base})
		return plan, nil
	}

	changes := slices.Clone(in.Changes)
	verdicts := make([]*types.Verdict, len(changes))
	own := make([]map[string]bool, len(changes))
	tops := make([]string, len(changes))
	if err := p.each(ctx, len(changes), func(ctx context.Context, i int) (err error) {
		verdicts[i], own[i], tops[i], err = r.fetch(ctx, changes[i])
		return err
	}); err != nil {
		return types.Plan{}, err
	}
	mergedOwn, err := r.mergedCommits(ctx)
	if err != nil {
		return types.Plan{}, err
	}
	unqueuedTops := make([]string, len(in.Unqueued))
	if r.stacking() {
		if err := p.each(ctx, len(in.Unqueued), func(ctx context.Context, i int) (err error) {
			unqueuedTops[i], err = r.unqueuedTop(ctx, in.Unqueued[i])
			return err
		}); err != nil {
			return types.Plan{}, err
		}
	}
	stacks := stackInput{changes: changes, own: own, tops: tops, merged: in.Merged, mergedOwn: mergedOwn,
		unqueued: in.Unqueued, unqueuedTops: unqueuedTops}
	stacks.detect(verdicts)
	if err := p.each(ctx, len(changes), func(ctx context.Context, i int) (err error) {
		if verdicts[i] == nil {
			verdicts[i], err = r.admit(ctx, &changes[i])
		}
		return err
	}); err != nil {
		return types.Plan{}, err
	}
	plan.Verdicts, plan.Partitions = arrange(changes, verdicts)
	for _, v := range plan.Verdicts {
		p.Events.Emit(Event{Kind: EventDecided, Change: v.Change.ID, Decision: v.Decision, Code: v.Code, Reason: v.Reason})
	}
	for gi, g := range plan.Partitions {
		ids := make([]string, len(g))
		for i, c := range g {
			ids[i] = c.ID
		}
		p.Events.Emit(Event{Kind: EventPartition, Partition: partitionOf(gi), Changes: ids})
	}
	return plan, nil
}

// each runs fn for 0..n-1, Parallel at once, stopping at the first error, which it
// returns.
func (p *Planner) each(ctx context.Context, n int, fn func(ctx context.Context, i int) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	parallel := p.Parallel
	if parallel == 0 {
		parallel = runtime.NumCPU()
	}
	sem := make(chan struct{}, parallel)
	var (
		once  sync.Once
		first error
		wg    sync.WaitGroup
	)
	fail := func(err error) {
		once.Do(func() { first = err })
		cancel()
	}
	for i := range n {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				fail(ctx.Err())
				return
			}
			defer func() { <-sem }()
			if err := fn(ctx, i); err != nil {
				fail(err)
			}
		})
	}
	wg.Wait()
	return first
}

// checkMerged refuses a merged change whose commit the base does not carry: the
// provider is wrong about what merged, and a stack base read from it would be too.
func (r *planning) checkMerged(ctx context.Context) error {
	for _, m := range r.in.Merged {
		if err := r.vcs.FetchCommit(ctx, r.clone.Root, r.clone.Remote, m.Commit); err != nil {
			return fmt.Errorf("fetch the commit #%s merged as: %w", m.ID, err)
		}
		on, err := r.vcs.IsAncestor(ctx, r.clone.Root, m.Commit, r.tip)
		if err != nil {
			return err
		}
		if !on {
			return fmt.Errorf("provider lists #%s as merged at %s, which %s does not carry", m.ID, short(m.Commit), r.in.Base)
		}
		if err := r.vcs.FetchCommit(ctx, r.clone.Root, r.clone.Remote, m.Head); err != nil {
			return fmt.Errorf("fetch the head #%s merged at: %w", m.ID, err)
		}
		r.heads = append(r.heads, m.Head)
	}
	for _, c := range r.in.Changes {
		r.heads = append(r.heads, c.Head)
	}
	return nil
}

// stacking says whether any change can be stacked on another or carry an unqueued one,
// which is what reading every change's own commits is for.
func (r *planning) stacking() bool { return len(r.heads) > 1 || len(r.in.Unqueued) > 0 }

// fetch refuses a fork, fetches c's head and, when stacks are possible, returns the
// commits c carries that the base does not, and its own top. A fork's head is fetched
// only then, to read its ancestry: a change built on the fork carries its commits, and
// has to wait on its kick-back whatever the fork merged in since. Nothing of the fork
// is built or run.
func (r *planning) fetch(ctx context.Context, c types.Change) (*types.Verdict, map[string]bool, string, error) {
	var refused *types.Verdict
	if c.Fork {
		refused = decided(c, types.DecisionKick, types.CodeKickRefused, "a change from a fork", forkReport)
		if !r.stacking() {
			return refused, nil, c.Head, nil
		}
	}
	if err := fetchHead(ctx, r.vcs, r.clone, c); err != nil {
		return nil, nil, "", fmt.Errorf("fetch %s: %w", c.Label(), err)
	}
	if !r.stacking() {
		return nil, nil, c.Head, nil
	}
	own, err := ownCommits(ctx, r.vcs, r.clone.Root, r.tip, c.Head)
	if err != nil {
		return nil, nil, "", fmt.Errorf("commits of %s: %w", c.Label(), err)
	}
	top, err := ownTop(ctx, r.vcs, r.clone.Root, r.tip, c.Head)
	if err != nil {
		return nil, nil, "", fmt.Errorf("commits of %s: %w", c.Label(), err)
	}
	return refused, own, top, nil
}

// unqueuedTop fetches u's head and returns its own top, which a change built on u
// before a merge of the base into it carries instead of the head. A fetch is local when
// the clone already holds the head, as a clone of every branch does for a change from
// this repository.
func (r *planning) unqueuedTop(ctx context.Context, u types.UnqueuedChange) (string, error) {
	if err := r.vcs.FetchCommit(ctx, r.clone.Root, r.clone.Remote, u.Head); err != nil {
		return "", fmt.Errorf("fetch the head of the unqueued #%s: %w", u.ID, err)
	}
	top, err := ownTop(ctx, r.vcs, r.clone.Root, r.tip, u.Head)
	if err != nil {
		return "", fmt.Errorf("commits of the unqueued #%s: %w", u.ID, err)
	}
	return top, nil
}

// ownTop is head with the merges of the base at tip into it peeled off: GitHub's
// "Update branch", or an update commit the queue pushed. A change stacked on this one
// before such a merge carries the top, not the head, and is still stacked on it.
func ownTop(ctx context.Context, v types.ReadVCS, root, tip, head string) (string, error) {
	for range reviewDepth {
		cm, err := v.FindCommit(ctx, root, head)
		if err != nil || len(cm.Parents) != 2 {
			return head, err
		}
		onBase, err := v.IsAncestor(ctx, root, cm.Parents[1], tip)
		if err != nil || !onBase {
			return head, err
		}
		head = cm.Parents[0]
	}
	return head, nil
}

// mergedCommits reads each merged change's own commits the base does not carry, newest
// first: none for a merge, which put its head on the base.
func (r *planning) mergedCommits(ctx context.Context) ([][]string, error) {
	if !r.stacking() {
		return nil, nil
	}
	out := make([][]string, len(r.in.Merged))
	for i, m := range r.in.Merged {
		commits, err := r.vcs.RangeCommits(ctx, r.clone.Root, r.tip, m.Head, nil)
		if err != nil {
			return nil, fmt.Errorf("commits of #%s: %w", m.ID, err)
		}
		for _, c := range commits {
			out[i] = append(out[i], c.ID)
		}
	}
	return out, nil
}

// admit checks c's approval, intent, method and merge onto the base, and asks for its
// affected set. It returns a verdict when planning settles c.
func (r *planning) admit(ctx context.Context, c *types.Change) (*types.Verdict, error) {
	merged, err := r.vcs.IsAncestor(ctx, r.clone.Root, c.Head, r.tip)
	if err != nil {
		return nil, err
	}
	if merged {
		return decided(*c, types.DecisionMerged, "", "its head is already on "+r.in.Base, ""), nil
	}
	stackBase := ""
	if c.Below == "" {
		stackBase = c.StackBase
	}
	a, err := approval(ctx, r.provider, r.vcs, r.facts, r.clone, r.tip, *c, stackBase, planRefs(r.in))
	if err != nil {
		return nil, err
	}
	if a.carried != "" {
		r.Events.Emit(Event{Kind: EventNotice, Change: c.ID, Reason: carriedNotice(a)})
	}
	if v := admission(c, a, r.caps); v != nil {
		return v, nil
	}
	paths, err := r.vcs.RangeFiles(ctx, r.clone.Root, r.tip, c.Head, nil)
	if err != nil {
		return nil, fmt.Errorf("changed files of %s: %w", c.Label(), err)
	}
	// A change stacked on an unmerged one would report that one's conflicts as its own;
	// building the candidate catches its own.
	if c.Below == "" {
		if err := checkMerge(ctx, r.vcs, r.facts, r.clone.Root, r.tip, *c); err != nil {
			conf, ok := asConflict(err)
			if !ok {
				return nil, fmt.Errorf("merge %s onto %s: %w", c.Label(), r.in.Base, err)
			}
			v := decided(*c, types.DecisionKick, types.CodeKickConflict, "", conflictReport(r.in.Base, c.Head))
			v.Reason, v.Paths, v.With = firstLine(v.Report), conf.paths, conf.with
			return v, nil
		}
	}
	if c.Affected == nil && c.UnboundedBy == "" {
		// Asking the build tool is the slowest step of a plan, which is why admission
		// runs side by side.
		affected, unboundedBy, err := r.facts.Affected(ctx, *c, paths)
		if err != nil {
			return nil, fmt.Errorf("affected set of %s: %w", c.Label(), err)
		}
		c.Affected, c.UnboundedBy = affected, unboundedBy
	}
	if c.AuthorRegenerates, err = authorRegenerates(ctx, r.facts, paths); err != nil {
		return nil, fmt.Errorf("regeneration of %s: %w", c.Label(), err)
	}
	return nil, nil //nolint:nilnil // no verdict is planning's answer that c is admitted
}

// authorRegenerates returns the generated files among changed whose regeneration the
// build tool cannot prove runs none of changed: the proof applying needs before it
// regenerates them itself.
func authorRegenerates(ctx context.Context, f types.BuildFacts, changed []string) ([]string, error) {
	generated, err := outputs(ctx, f, changed)
	if err != nil || len(generated) == 0 {
		return nil, err
	}
	g, err := f.Generation(ctx, generated, changed)
	if err != nil {
		return nil, fmt.Errorf("generation of %s: %w", joinPaths(generated), err)
	}
	if regenerationProven(g) {
		return nil, nil
	}
	return generated, nil
}

// planRefs is every change in is a listed change may carry the head of.
func planRefs(in types.Changes) []stackRef {
	return stackRefs(types.Plan{Merged: in.Merged, Unqueued: in.Unqueued, Partitions: [][]types.Change{in.Changes}})
}

func decided(c types.Change, d types.Decision, code types.Code, reason, report string) *types.Verdict {
	return &types.Verdict{Change: c, Decision: d, Code: code, Reason: reason, Report: report}
}

func refused(c types.Change, reason string) *types.Verdict {
	return decided(c, types.DecisionKick, types.CodeKickRefused, reason, "The merge queue cannot merge this change: "+reason+".\n")
}

const forkReport = "The merge queue does not merge changes from forks: it cannot push their " +
	"update commits, and it runs only code whose author can push to this repository. " +
	"Ask a maintainer to push the branch here.\n"

// conflictReport says what conflicts on which commits; the files and the base's commits
// that touched them travel beside it in the verdict's Paths and With.
func conflictReport(base, commit string) string {
	return fmt.Sprintf("The merge queue could not merge this change at `%s`: it conflicts with `%s` outside the generated files.\n\n"+
		"Merge `%s` into this branch and resolve the conflict by hand.\n", short(commit), base, base)
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
