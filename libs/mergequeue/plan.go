package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Planner admits changes and partitions them. It executes no change's code and writes
// nothing to the provider: it calls only [Provider.Describe] and [Provider.ApprovalAt].
type Planner struct {
	// Provider checks approval at each head and says which merge methods the repository
	// allows. Nil admits every change unchecked, for a caller that vouched for its
	// input; an Applier re-checks approval regardless.
	Provider Provider
	// Facts is asked about a change whose input carries no affected set. Nil leaves such
	// a change unbounded.
	Facts BuildFacts
	// Depth is how many candidates of one partition validate at once. Zero means 1.
	Depth int
	// Parallel is how many changes are admitted (fetched, checked, and put to Facts) at
	// once. Zero means 1.
	Parallel int
	Events   *Events

	vcs VCS
}

// NewPlanner plans against v.
func NewPlanner(v VCS) *Planner { return &Planner{vcs: v} }

// planning is one Run's state.
type planning struct {
	*Planner
	in    Changes
	tip   string
	caps  Capabilities
	heads []string // every head a change may be stacked on: listed and landed
}

// Run plans in. Changes are admitted concurrently, but the plan lists them in queue
// order, each after the change it is stacked on.
func (p *Planner) Run(ctx context.Context, in Changes) (Plan, error) {
	if p.vcs == nil {
		return Plan{}, errors.New("a Planner needs a VCS; build it with NewPlanner")
	}
	if p.Depth < 0 || p.Parallel < 0 {
		return Plan{}, fmt.Errorf("depth %d and parallel %d must not be negative", p.Depth, p.Parallel)
	}
	if err := in.check(); err != nil {
		return Plan{}, err
	}
	tip, err := p.vcs.FetchRef(ctx, branchRef(in.Base))
	if err != nil {
		return Plan{}, fmt.Errorf("resolve %s: %w", in.Base, err)
	}
	r := &planning{Planner: p, in: in, tip: tip}
	if p.Provider != nil {
		if r.caps, err = p.Provider.Describe(ctx, ListQuery{Base: in.Base, Remote: in.Remote}); err != nil {
			return Plan{}, fmt.Errorf("describe the provider: %w", err)
		}
		if err := r.caps.check(); err != nil {
			return Plan{}, err
		}
	}
	if err := r.checkLanded(ctx); err != nil {
		return Plan{}, err
	}
	plan := Plan{Schema: SchemaPlan, Base: in.Base, Remote: in.Remote, BaseCommit: tip, Depth: max(1, p.Depth), Landed: in.Landed}
	if len(in.Changes) == 0 {
		p.Events.Emit(Event{Kind: EventNotice, Reason: "no change carries merge intent against " + in.Base})
		return plan, nil
	}

	changes := slices.Clone(in.Changes)
	verdicts := make([]*Verdict, len(changes))
	own := make([]map[string]bool, len(changes))
	tops := make([]string, len(changes))
	if err := p.each(ctx, len(changes), func(ctx context.Context, i int) (err error) {
		verdicts[i], own[i], tops[i], err = r.fetch(ctx, changes[i])
		return err
	}); err != nil {
		return Plan{}, err
	}
	landedOwn, err := r.landedCommits(ctx)
	if err != nil {
		return Plan{}, err
	}
	for i := range changes {
		if verdicts[i] == nil {
			verdicts[i] = r.stack(&changes[i], changes, own, tops, landedOwn)
		}
	}
	if err := p.each(ctx, len(changes), func(ctx context.Context, i int) (err error) {
		if verdicts[i] == nil {
			verdicts[i], err = r.admit(ctx, &changes[i])
		}
		return err
	}); err != nil {
		return Plan{}, err
	}
	cascade(changes, verdicts)

	var admitted []Change
	for i, c := range changes {
		if v := verdicts[i]; v != nil {
			plan.Verdicts = append(plan.Verdicts, *v)
			p.Events.Emit(Event{Kind: EventDecided, Change: c.ID, Decision: v.Decision, Code: v.Code, Reason: v.Reason})
			continue
		}
		admitted = append(admitted, c)
	}
	plan.Partitions = Partition(stackOrder(admitted))
	for gi, g := range plan.Partitions {
		ids := make([]string, len(g))
		for i, c := range g {
			ids[i] = c.ID
		}
		p.Events.Emit(Event{Kind: EventPartition, Partition: partitionOf(gi), Changes: ids})
	}
	return plan, nil
}

// each runs fn for 0..n-1, Parallel at once, stopping at the first error.
func (p *Planner) each(ctx context.Context, n int, fn func(ctx context.Context, i int) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, max(1, p.Parallel))
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			defer func() { <-sem }()
			if errs[i] = fn(ctx, i); errs[i] != nil {
				cancel()
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

// checkLanded refuses a landed change whose commit the base does not carry: the provider
// is wrong about what landed, and a stack base read from it would be too.
func (r *planning) checkLanded(ctx context.Context) error {
	for _, l := range r.in.Landed {
		if err := r.vcs.FetchCommit(ctx, l.Commit); err != nil {
			return fmt.Errorf("fetch the commit #%s landed as: %w", l.ID, err)
		}
		on, err := r.vcs.IsAncestor(ctx, l.Commit, r.tip)
		if err != nil {
			return err
		}
		if !on {
			return fmt.Errorf("the provider lists #%s as landed at %s, which %s does not carry", l.ID, short(l.Commit), r.in.Base)
		}
		if err := r.vcs.FetchCommit(ctx, l.Head); err != nil {
			return fmt.Errorf("fetch the head #%s landed at: %w", l.ID, err)
		}
		r.heads = append(r.heads, l.Head)
	}
	for _, c := range r.in.Changes {
		r.heads = append(r.heads, c.Head)
	}
	return nil
}

// stacking says whether any change can be stacked on another, which is what reading
// every change's own commits is for.
func (r *planning) stacking() bool { return len(r.heads) > 1 }

// fetch refuses a fork, fetches c's head and, when stacks are possible, returns the
// commits c carries that the base does not, and its own top.
func (r *planning) fetch(ctx context.Context, c Change) (*Verdict, map[string]bool, string, error) {
	if c.Fork {
		return decided(c, DecisionKick, CodeRefused, "a change from a fork", forkReport), nil, "", nil
	}
	if err := fetchHead(ctx, r.vcs, c); err != nil {
		return nil, nil, "", fmt.Errorf("fetch %s: %w", c.Label(), err)
	}
	if !r.stacking() {
		return nil, nil, c.Head, nil
	}
	own, err := r.ownCommits(ctx, c.Head)
	if err != nil {
		return nil, nil, "", fmt.Errorf("commits of %s: %w", c.Label(), err)
	}
	top, err := r.ownTop(ctx, c.Head)
	if err != nil {
		return nil, nil, "", fmt.Errorf("commits of %s: %w", c.Label(), err)
	}
	return nil, own, top, nil
}

// ownTop is head with the merges of the base into it peeled off: GitHub's "Update
// branch", or an update commit the queue pushed. A change stacked on this one before
// such a merge carries the top, not the head, and is still stacked on it.
func (r *planning) ownTop(ctx context.Context, head string) (string, error) {
	for range reviewDepth {
		cm, err := r.vcs.FindCommit(ctx, head)
		if err != nil || len(cm.Parents) != 2 {
			return head, err
		}
		onBase, err := r.vcs.IsAncestor(ctx, cm.Parents[1], r.tip)
		if err != nil || !onBase {
			return head, err
		}
		head = cm.Parents[0]
	}
	return head, nil
}

func (r *planning) ownCommits(ctx context.Context, head string) (map[string]bool, error) {
	commits, err := r.vcs.RangeCommits(ctx, r.tip, head, nil)
	if err != nil {
		return nil, err
	}
	own := make(map[string]bool, len(commits))
	for _, c := range commits {
		own[c.ID] = true
	}
	return own, nil
}

// landedCommits reads each landed change's own commits the base does not carry, newest
// first: none for a merge, which put its head on the base.
func (r *planning) landedCommits(ctx context.Context) ([][]string, error) {
	if !r.stacking() {
		return nil, nil
	}
	out := make([][]string, len(r.in.Landed))
	for i, l := range r.in.Landed {
		commits, err := r.vcs.RangeCommits(ctx, r.tip, l.Head, nil)
		if err != nil {
			return nil, fmt.Errorf("commits of #%s: %w", l.ID, err)
		}
		for _, c := range commits {
			out[i] = append(out[i], c.ID)
		}
	}
	return out, nil
}

// stackNode is a change another may be stacked on.
type stackNode struct {
	id     string
	head   string
	method MergeMethod
	open   bool
	own    map[string]bool
}

// stack finds what c is stacked on, from ancestry: a listed or landed change whose head
// c carries and the base does not. It records the nearest one's head as c's stack base
// and, while that one is unlanded, its id as Below; the provider's declared parent must
// agree. It returns a verdict when c cannot be admitted as stacked.
func (r *planning) stack(c *Change, changes []Change, own []map[string]bool, tops []string, landedOwn [][]string) *Verdict {
	c.StackBase, c.Below = "", ""
	var mine map[string]bool
	var nodes []stackNode
	for i, o := range changes {
		if o.ID == c.ID {
			mine = own[i]
			continue
		}
		nodes = append(nodes, stackNode{id: o.ID, head: tops[i], method: o.Method, open: true, own: own[i]})
	}
	for i, l := range r.in.Landed {
		// A change landed through an update commit is reported at that commit, which a
		// change stacked on it does not carry: its stack base is the newest of the
		// landed change's own commits it does carry.
		n := stackNode{id: l.ID, head: l.Head, method: l.Method, own: map[string]bool{}}
		for _, id := range landedOwn[i] {
			n.own[id] = true
		}
		if j := slices.IndexFunc(landedOwn[i], func(id string) bool { return mine[id] }); j >= 0 {
			n.head = landedOwn[i][j]
		}
		nodes = append(nodes, n)
	}
	var below []stackNode
	for _, n := range nodes {
		if mine[n.head] {
			below = append(below, n)
		}
	}
	declared := func() *Verdict {
		if c.Parent == "" {
			return nil
		}
		if !slices.ContainsFunc(nodes, func(n stackNode) bool { return n.id == c.Parent }) {
			return decided(*c, DecisionWait, CodeParent, "stacked on #"+c.Parent+", which is not queued", "")
		}
		return decided(*c, DecisionWait, CodeRestack, "not built on #"+c.Parent+"'s head; restack it (gh stack rebase, gt restack or av sync)", "")
	}
	if len(below) == 0 {
		return declared()
	}
	var nearest *stackNode
	for i, n := range below {
		if !slices.ContainsFunc(below, func(o stackNode) bool { return o.id != n.id && !n.own[o.head] }) {
			nearest = &below[i]
			break
		}
	}
	if nearest == nil {
		a, b := incomparable(below)
		return refused(*c, "built on #"+a+" and #"+b+", which are not stacked on each other")
	}
	if c.Parent != "" && c.Parent != nearest.id {
		return declared()
	}
	unlanded := 0
	for _, n := range below {
		if n.open {
			unlanded++
		}
	}
	switch {
	case unlanded > MaxStackDepth:
		return refused(*c, fmt.Sprintf("stacked on %d unlanded changes; the queue lands stacks up to %d deep", unlanded, MaxStackDepth))
	case c.Method == MethodRebase:
		return refused(*c, "a stacked change cannot land with the rebase method yet: predicting it needs git replay, which git still marks experimental")
	case nearest.method != c.Method:
		return refused(*c, fmt.Sprintf("the stack mixes %s (#%s) and %s (#%s); a stack lands with one merge method", nearest.method, nearest.id, c.Method, c.ID))
	}
	c.StackBase = nearest.head
	if nearest.open {
		c.Below = nearest.id
	}
	return nil
}

func incomparable(below []stackNode) (string, string) {
	for _, a := range below {
		for _, b := range below {
			if a.id != b.id && !a.own[b.head] && !b.own[a.head] {
				return a.id, b.id
			}
		}
	}
	return below[0].id, below[len(below)-1].id
}

// admit checks c's approval, method and merge onto the base, and asks for its affected
// set. It returns a verdict when planning settles c.
func (r *planning) admit(ctx context.Context, c *Change) (*Verdict, error) {
	if r.Provider != nil {
		a, err := approval(ctx, r.Provider, r.vcs, r.tip, r.tip, *c, r.heads)
		if err != nil {
			return nil, err
		}
		if a.carried != "" {
			r.Events.Emit(Event{Kind: EventNotice, Change: c.ID, Reason: carriedNotice(a)})
		}
		if a.Head != c.Head {
			c.Head = a.Head
			return decided(*c, DecisionWait, CodeHeadMoved, "head moved to "+short(a.Head)+" while listing; retried next run", ""), nil
		}
		if !a.Approved {
			return decided(*c, DecisionWait, CodeNotApproved, "not approved at "+short(a.reviewed)+reasonSuffix(a.Reason), ""), nil
		}
		if !r.caps.allows(c.Method) {
			return refused(*c, "the repository does not allow the "+string(c.Method)+" merge method; pick one it does"), nil
		}
	}
	merged, err := r.vcs.IsAncestor(ctx, c.Head, r.tip)
	if err != nil {
		return nil, err
	}
	if merged {
		return decided(*c, DecisionWait, CodeMerged, "its head is already on "+r.in.Base, ""), nil
	}
	paths, err := r.vcs.RangeFiles(ctx, r.tip, c.Head)
	if err != nil {
		return nil, fmt.Errorf("changed files of %s: %w", c.Label(), err)
	}
	// A change stacked on an unlanded one would report that one's conflicts as its own;
	// building the candidate catches its own.
	if c.Below == "" {
		if err := checkMerge(ctx, r.vcs, r.tip, r.tip, *c); err != nil {
			conf, ok := asConflict(err)
			if !ok {
				return nil, fmt.Errorf("merge %s onto %s: %w", c.Label(), r.in.Base, err)
			}
			v := decided(*c, DecisionKick, CodeConflict, "", conflictReport(r.in.Base, c.Head, conf))
			v.Reason, v.Paths, v.With = firstLine(v.Report), conf.Paths, conf.With
			return v, nil
		}
	}
	if c.Affected == nil && c.UnboundedBy == "" && r.Facts != nil {
		// Asking the build tool is the slowest step of a plan, which is why admission
		// runs side by side.
		affected, unboundedBy, err := r.Facts.Affected(ctx, *c, paths)
		if err != nil {
			return nil, fmt.Errorf("affected set of %s: %w", c.Label(), err)
		}
		c.Affected, c.UnboundedBy = affected, unboundedBy
	}
	return nil, nil
}

// cascade holds every change stacked on one planning settled: it waits, and is never
// blamed for what the change beneath it did.
func cascade(changes []Change, verdicts []*Verdict) {
	index := make(map[string]int, len(changes))
	for i, c := range changes {
		index[c.ID] = i
	}
	var hold func(i int) *Verdict
	hold = func(i int) *Verdict {
		if verdicts[i] != nil || changes[i].Below == "" {
			return verdicts[i]
		}
		j, ok := index[changes[i].Below]
		if !ok {
			return nil
		}
		below := hold(j)
		switch {
		case below == nil:
			return nil
		case below.Code == CodeParentKicked:
			verdicts[i] = decided(changes[i], DecisionWait, CodeParentKicked, below.Reason, "")
		case below.Decision == DecisionKick:
			verdicts[i] = decided(changes[i], DecisionWait, CodeParentKicked, kickedBelow(changes[j].ID), "")
		default:
			verdicts[i] = decided(changes[i], DecisionWait, CodeParent, "stacked on #"+changes[j].ID+", which is waiting: "+below.Reason, "")
		}
		return verdicts[i]
	}
	for i := range changes {
		hold(i)
	}
}

// kickedBelow is the wait of a change stacked, directly or not, on kicked.
func kickedBelow(kicked string) string {
	return "#" + kicked + " was kicked back; this stays queued and is validated again once it returns"
}

// stackOrder keeps queue order but moves each change after the one it is stacked on.
func stackOrder(changes []Change) []Change {
	index := make(map[string]int, len(changes))
	for i, c := range changes {
		index[c.ID] = i
	}
	out := make([]Change, 0, len(changes))
	done := make([]bool, len(changes))
	var emit func(i int)
	emit = func(i int) {
		if done[i] {
			return
		}
		done[i] = true
		if j, ok := index[changes[i].Below]; ok {
			emit(j)
		}
		out = append(out, changes[i])
	}
	for i := range changes {
		emit(i)
	}
	return out
}

func decided(c Change, d Decision, code Code, reason, report string) *Verdict {
	return &Verdict{Change: c, Decision: d, Code: code, Reason: reason, Report: report}
}

func refused(c Change, reason string) *Verdict {
	return decided(c, DecisionKick, CodeRefused, reason, "The merge queue cannot merge this change: "+reason+".\n")
}

// approvalResult is an approval with what it was asked about.
type approvalResult struct {
	Approval
	reviewed string              // the commit asked about
	owed     []regenerationProof // what reviewed covers only once regeneration proves it
	carried  string              // the older commit an approval was carried over from
}

// approval asks prov for c's approval at the commit a review of c.Head covers. When the
// approval stands only at an older commit, it carries over if c.Head is that commit
// rebased with nothing else changed: heads names the changes c may be stacked on, which
// is where that commit's own delta starts. A provider that reports no head, base or
// method is broken: without them the queue cannot tell what it would merge.
func approval(ctx context.Context, prov Provider, v VCS, baseCommit, tip string, c Change, heads []string) (approvalResult, error) {
	reviewed, owed, err := reviewTarget(ctx, v, baseCommit, tip, c.Head, c.StackBase)
	if err != nil {
		return approvalResult{}, fmt.Errorf("review target of %s: %w", c.Label(), err)
	}
	a, err := prov.ApprovalAt(ctx, c, reviewed)
	if err != nil {
		return approvalResult{}, fmt.Errorf("approval of %s: %w", c.Label(), err)
	}
	switch {
	case a.Head == "":
		return approvalResult{}, fmt.Errorf("approval of %s: the provider reported no head", c.Label())
	case a.Base == "" || !a.Method.valid():
		return approvalResult{}, fmt.Errorf("approval of %s: the provider reported base %q and merge method %q; both are required", c.Label(), a.Base, a.Method)
	}
	res := approvalResult{Approval: a, reviewed: reviewed, owed: owed}
	if a.Approved || a.ApprovedAt == "" || a.ApprovedAt == reviewed || a.Head != c.Head {
		return res, nil
	}
	if !isObjectID(a.ApprovedAt) {
		return approvalResult{}, fmt.Errorf("approval of %s: the provider reported approvals at %q, not a commit id", c.Label(), a.ApprovedAt)
	}
	ok, err := rebasedFrom(ctx, v, tip, a.ApprovedAt, reviewed, c.StackBase, heads)
	if err != nil {
		return approvalResult{}, fmt.Errorf("compare %s with its approved %s: %w", c.Label(), short(a.ApprovedAt), err)
	}
	if ok {
		res.Approved, res.Reason, res.carried = true, "", a.ApprovedAt
	}
	return res, nil
}

// rebasedFrom reports whether now is old moved onto a new base with its own delta
// unchanged. Each one's delta starts at the change it is stacked on, else where it left
// the base.
func rebasedFrom(ctx context.Context, v VCS, tip, old, now, stackBase string, heads []string) (bool, error) {
	if err := v.FetchCommit(ctx, old); err != nil {
		return false, err
	}
	oldBase, err := stackBaseOf(ctx, v, tip, old, heads)
	if err != nil {
		return false, err
	}
	if oldBase == "" {
		if oldBase, err = forkPoint(ctx, v, tip, old); err != nil {
			return false, err
		}
	}
	newBase := stackBase
	if newBase == "" {
		if newBase, err = forkPoint(ctx, v, tip, now); err != nil {
			return false, err
		}
	}
	return trivialRebase(ctx, v, oldBase, old, newBase, now)
}

// stackBaseOf is the nearest of heads that x carries and tip does not, or "".
func stackBaseOf(ctx context.Context, v VCS, tip, x string, heads []string) (string, error) {
	var below []string
	for _, h := range heads {
		if h == x {
			continue
		}
		in, err := v.IsAncestor(ctx, h, x)
		if err != nil {
			return "", err
		}
		if !in {
			continue
		}
		on, err := v.IsAncestor(ctx, h, tip)
		if err != nil {
			return "", err
		}
		if !on {
			below = append(below, h)
		}
	}
	nearest := ""
	for _, h := range below {
		if nearest == "" {
			nearest = h
			continue
		}
		newer, err := v.IsAncestor(ctx, nearest, h)
		if err != nil {
			return "", err
		}
		if newer {
			nearest = h
		}
	}
	return nearest, nil
}

func carriedNotice(a approvalResult) string {
	return "head " + short(a.reviewed) + " is " + short(a.carried) + " rebased with its diff unchanged; its approval carried over"
}

const forkReport = "The merge queue does not merge changes from forks: it cannot push their " +
	"update commits, and it runs only code whose author can push to this repository. " +
	"Ask a maintainer to push the branch here.\n"

func conflictReport(base, commit string, conf Conflict) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The merge queue could not merge this change at `%s`: it conflicts with `%s` in files that are not generated.\n\n", short(commit), base)
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
