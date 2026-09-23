package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Applier is the write step of a queue run. It trusts validation's verdicts only as far
// as the plan vouches for them, and re-checks everything it can without running any
// change's code: approval at the head, that the head has not moved, the change's base
// and merge method, that the provider's merge will carry exactly the validated tree, and
// afterwards that the base branch does and has the shape the merge method gives.
//
// The status it posts reads success only once a change has merged. A required status
// that goes green before the merge would let anyone merge the change on whatever the
// base branch is by then, so the credential an Applier merges with must be one branch
// protection lets bypass the queue's own status check.
type Applier struct {
	// StatusContext names the commit status an Applier posts; empty means
	// [DefaultStatusContext].
	StatusContext string
	// Interval is how long to wait between polls while verdicts are outstanding.
	Interval time.Duration
	// DryRun reports what would merge and calls nothing on the provider.
	DryRun bool
	// Committer commits every update commit; its author is the change head's author.
	// Zero means the queue's own identity.
	Committer Person
	Events    *Events

	provider Provider
	vcs      VCS
	src      VerdictSource
}

// NewApplier merges through provider and v the verdicts src supplies.
func NewApplier(provider Provider, v VCS, src VerdictSource) *Applier {
	return &Applier{provider: provider, vcs: v, src: src}
}

// Run merges plan's changes as their verdicts arrive, each as its own commit, as soon as
// every change beneath it in its partition has merged; a run of stacked changes lands in
// one call where the provider lands stacks atomically. Partitions merge independently. It
// returns once every admitted change is settled or the source is exhausted; what it did
// not reach stays queued for the next run. An error means applying stopped.
func (a *Applier) Run(ctx context.Context, plan Plan) error {
	if a.provider == nil || a.vcs == nil || a.src == nil {
		return errors.New("an Applier needs a Provider, a VCS and a VerdictSource; build it with NewApplier")
	}
	if err := plan.check(); err != nil {
		return err
	}
	if a.Committer == (Person{}) {
		a.Committer = queueIdentity
	}
	if a.Committer.Name == "" || a.Committer.Email == "" {
		return fmt.Errorf("committer %q <%s> needs both a name and an email", a.Committer.Name, a.Committer.Email)
	}
	r := &applyRun{Applier: a, plan: plan, merged: map[string]bool{}, got: map[string]Verdict{}}
	if !a.DryRun {
		caps, err := a.provider.Describe(ctx, ListQuery{Base: plan.Base, Remote: plan.Remote})
		if err != nil {
			return fmt.Errorf("describe the provider: %w", err)
		}
		if err := caps.check(); err != nil {
			return err
		}
		r.caps = caps
	}
	for _, v := range plan.Verdicts {
		if err := r.settle(ctx, v); err != nil {
			return err
		}
	}
	queues := slices.Clone(plan.Partitions)
	for {
		fresh, done, err := a.src.Poll(ctx)
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
				n, err := r.settleFront(ctx, queues[gi], done)
				if err != nil {
					return err
				}
				if n == 0 {
					break
				}
				queues[gi] = queues[gi][n:]
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
					if err := r.wait(ctx, c, c.Head, CodeBehind, "not validated in this run"); err != nil {
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
		case <-time.After(max(a.Interval, 10*time.Millisecond)):
		}
	}
}

type applyRun struct {
	*Applier
	plan   Plan
	caps   Capabilities
	merged map[string]bool
	got    map[string]Verdict
}

// accept files a verdict under the plan's own record of its change. A verdict names
// its change, but only the plan says which head was admitted and what lies beneath
// it, so a verdict that disagrees with the plan merges nothing.
func (r *applyRun) accept(v Verdict) error {
	gi, pos, ok := r.plan.Find(v.Change.ID)
	if !ok {
		return fmt.Errorf("a verdict names #%s, which the plan did not admit", v.Change.ID)
	}
	planned := r.plan.Partitions[gi][pos]
	wait := func(reason string) Verdict {
		return Verdict{Change: planned, Decision: DecisionWait, Code: CodeRevalidate, Reason: reason}
	}
	switch {
	case v.Change.Head != planned.Head:
		v = wait("validated at " + short(v.Change.Head) + ", not the planned head " + short(planned.Head))
	case v.Decision == DecisionMerge && v.After != "" && !slices.ContainsFunc(r.plan.Partitions[gi][:pos], func(c Change) bool { return c.ID == v.After }):
		v = wait("validated on top of #" + v.After + ", which is not beneath it in its partition")
	case v.Decision == DecisionMerge && v.After == "" && v.Onto != r.plan.BaseCommit:
		v = wait("validated at the bottom of its partition, but onto " + short(v.Onto) + ", not the plan's base")
	case v.Decision == DecisionMerge && planned.Below != "" && v.After == "":
		v = wait("validated without #" + planned.Below + ", which it is stacked on")
	default:
		v.Change = planned
	}
	r.got[planned.ID] = v
	return nil
}

// settleFront settles what it can from the front of one partition's queue and returns
// how many changes that was: none while the front's verdict, or a stack run's, is still
// to come.
func (r *applyRun) settleFront(ctx context.Context, q []Change, done bool) (int, error) {
	v, ok := r.got[q[0].ID]
	if !ok {
		return 0, nil
	}
	run := r.stackRun(q)
	if len(run) == 1 {
		return 1, r.settle(ctx, v)
	}
	missing := slices.ContainsFunc(run, func(c Change) bool { _, ok := r.got[c.ID]; return !ok })
	if missing && !done {
		return 0, nil
	}
	return r.landRun(ctx, run)
}

// stackRun is the run of stacked changes at q's front that land in one provider call: each
// stacked on the one before as the provider declared, where the provider lands stacks
// atomically.
func (r *applyRun) stackRun(q []Change) []Change {
	if r.caps.StackMerge != StackMergeAtomic || r.DryRun {
		return q[:1]
	}
	n := 1
	for n < len(q) && q[n].Below == q[n-1].ID && q[n].Parent == q[n-1].ID {
		n++
	}
	return q[:n]
}

func (r *applyRun) settle(ctx context.Context, v Verdict) error {
	c := v.Change
	switch v.Decision {
	case DecisionKick:
		return r.kick(ctx, c, c.Head, v.kick())
	case DecisionWait:
		return r.wait(ctx, c, c.Head, v.Code, v.Reason)
	case DecisionMerge:
		if v.After != "" && !r.merged[v.After] {
			code := CodeBehind
			if v.After == c.Below {
				code = CodeParent
			}
			return r.wait(ctx, c, c.Head, code, "validated on top of #"+v.After+", which did not merge")
		}
		if v.After != "" && r.got[v.After].Candidate != v.Onto {
			return r.wait(ctx, c, c.Head, CodeRevalidate, "validated onto "+short(v.Onto)+", not the candidate #"+v.After+" merged from")
		}
		ok, err := r.merge(ctx, v)
		r.merged[c.ID] = ok
		return err
	}
	return fmt.Errorf("a verdict decides %q for %s", v.Decision, c.Label())
}

// ready is what merging one change needs once its checks passed.
type ready struct {
	v      Verdict
	tip    string
	tree   string // the tree the base carries once it lands
	commit string // the commit handed to the provider
}

// check re-checks v's change against the provider and the base at tip without writing
// anything, and returns the tree it lands as. A nil ready with a nil error means it
// waits or was kicked back.
func (r *applyRun) check(ctx context.Context, v Verdict, tip, onto string) (*ready, error) {
	c := v.Change
	if v.CandidateFile != "" {
		if err := r.vcs.Unbundle(ctx, v.CandidateFile); err != nil {
			return nil, fmt.Errorf("import the candidate of %s: %w", c.Label(), err)
		}
	}
	if err := fetchHead(ctx, r.vcs, c); err != nil {
		return nil, fmt.Errorf("fetch %s: %w", c.Label(), err)
	}
	a, err := approval(ctx, r.provider, r.vcs, r.plan.BaseCommit, tip, c, r.plan.heads())
	if err != nil {
		return nil, err
	}
	if a.carried != "" {
		r.Events.Emit(Event{Kind: EventNotice, Change: c.ID, Reason: carriedNotice(a)})
	}
	switch {
	case a.Head != c.Head:
		moved := c
		moved.Head = a.Head
		return nil, r.wait(ctx, moved, a.Head, CodeHeadMoved, "head moved to "+short(a.Head)+" after validation; retried next run")
	case !a.Approved:
		return nil, r.wait(ctx, c, c.Head, CodeNotApproved, "approval at "+short(a.reviewed)+" was withdrawn"+reasonSuffix(a.Reason))
	case len(a.owed) > 0 && v.Reviewed != a.reviewed:
		return nil, r.wait(ctx, c, c.Head, CodeNotApproved, "not approved at "+short(a.owed[0].Commit)+": validation did not prove regeneration reproduces its generated files")
	}
	if a.Base != r.plan.Base {
		retargeted, err := r.retarget(ctx, c, tip)
		if err != nil || retargeted == nil {
			return nil, err
		}
		a = *retargeted
	}
	switch {
	case a.Method != v.Method:
		return nil, r.wait(ctx, c, c.Head, CodeMethodChanged, "validated as "+string(v.Method)+", now "+string(a.Method)+"; validated again next run")
	case !r.caps.allows(a.Method):
		return nil, r.kick(ctx, c, c.Head, refusal("the repository does not allow the "+string(a.Method)+" merge method; pick one it does", nil))
	}
	tree, err := predict(ctx, r.vcs, tip, onto, v.Candidate, c)
	if conf, ok := asConflict(err); ok {
		return nil, r.wait(ctx, c, c.Head, CodeRevalidate, joinPaths(conf.Paths)+" changed both here and in what merged since validation; validated again next run")
	}
	if err != nil {
		return nil, fmt.Errorf("predict %s after %s: %w", r.plan.Base, c.Label(), err)
	}
	return &ready{v: v, tip: tip, tree: tree}, nil
}

// retarget asks the provider to point c at the plan's base, since the provider merges a change
// into its own base, and reads the approval again.
func (r *applyRun) retarget(ctx context.Context, c Change, tip string) (*approvalResult, error) {
	if err := r.provider.Retarget(ctx, c, r.plan.Base); err != nil {
		return nil, fmt.Errorf("retarget %s at %s: %w", c.Label(), r.plan.Base, err)
	}
	a, err := approval(ctx, r.provider, r.vcs, r.plan.BaseCommit, tip, c, r.plan.heads())
	if err != nil {
		return nil, err
	}
	if a.Base != r.plan.Base || a.Head != c.Head || !a.Approved {
		return nil, r.wait(ctx, c, c.Head, CodeRetarget, "targets "+a.Base+", not "+r.plan.Base+"; asked the provider to retarget it")
	}
	return &a, nil
}

func (r *applyRun) merge(ctx context.Context, v Verdict) (bool, error) {
	c := v.Change
	if v.BaseCommit != r.plan.BaseCommit {
		return false, r.wait(ctx, c, c.Head, CodeRevalidate, "validated on "+short(v.BaseCommit)+", not this plan's base "+short(r.plan.BaseCommit))
	}
	if r.DryRun {
		r.Events.Emit(Event{Kind: EventMerged, Change: c.ID, Commit: v.Candidate, Reason: "dry run: would merge candidate " + short(v.Candidate)})
		return true, nil
	}
	tip, err := r.vcs.FetchRef(ctx, branchRef(r.plan.Base))
	if err != nil {
		return false, fmt.Errorf("resolve %s: %w", r.plan.Base, err)
	}
	rd, err := r.check(ctx, v, tip, v.Onto)
	if rd == nil || err != nil {
		return false, err
	}
	return r.land(ctx, rd)
}

// land hands rd's change to the provider and checks what the base carries afterwards.
func (r *applyRun) land(ctx context.Context, rd *ready) (bool, error) {
	v, c, tip := rd.v, rd.v.Change, rd.tip
	commit, err := r.handOver(ctx, rd)
	var refused *RefusedError
	var held *WaitError
	switch {
	case errors.As(err, &refused):
		return false, r.kick(ctx, c, c.Head, refusal(refused.Reason, refused.Paths))
	case errors.As(err, &held):
		return false, r.wait(ctx, c, c.Head, held.Code, held.Reason)
	case err != nil:
		return false, fmt.Errorf("push the update commit of %s: %w", c.Label(), err)
	}
	if err := r.post(ctx, c, commit, StatePending, "applying candidate "+short(v.Candidate)); err != nil {
		return false, err
	}
	if err := r.provider.MergeChange(ctx, c, MergeRequest{Commit: commit, Message: v.Message}); err != nil {
		return false, r.wait(ctx, c, commit, CodeHostRefused, "the host refused the merge: "+err.Error())
	}
	after, err := r.vcs.FetchRef(ctx, branchRef(r.plan.Base))
	if err != nil {
		return true, fmt.Errorf("resolve %s after merging %s: %w", r.plan.Base, c.Label(), err)
	}
	r.Events.Emit(Event{Kind: EventMerged, Change: c.ID, Commit: commit})
	if err := r.landedAs(ctx, c, tip, after, commit, rd.tree, v.Method); err != nil {
		return true, err
	}
	if err := r.post(ctx, c, commit, StateSuccess, "merged as "+short(after)); err != nil {
		// The merge stands and the base carries the validated tree; the status is a record.
		r.Events.Emit(Event{Kind: EventNotice, Change: c.ID, Reason: err.Error()})
	}
	return true, nil
}

// landedAs stops applying unless after, the base once c merged onto tip, carries the
// validated tree in the shape c's merge method gives: something wrote to the branch
// besides the queue, or the provider merged differently than predicted, and nothing more
// may merge on a base nobody validated.
func (r *applyRun) landedAs(ctx context.Context, c Change, tip, after, commit, tree string, m MergeMethod) error {
	got, err := r.vcs.TreeID(ctx, after)
	if err != nil {
		return err
	}
	if got != tree {
		return fmt.Errorf("%s merged, but %s at %s carries tree %s, not the validated %s; stopping",
			c.Label(), r.plan.Base, short(after), short(got), short(tree))
	}
	cm, err := r.vcs.FindCommit(ctx, after)
	if err != nil {
		return err
	}
	shaped := false
	switch m {
	case MethodSquash:
		shaped = slices.Equal(cm.Parents, []string{tip})
	case MethodMerge:
		shaped = slices.Equal(cm.Parents, []string{tip, commit})
	case MethodRebase:
		shaped, err = r.linearOn(ctx, tip, after)
		if err != nil {
			return err
		}
	}
	if !shaped {
		return fmt.Errorf("%s merged, but %s at %s is not what a %s merge onto %s makes; stopping",
			c.Label(), r.plan.Base, short(after), m, short(tip))
	}
	return nil
}

// linearOn reports whether after is a line of single-parent commits on tip.
func (r *applyRun) linearOn(ctx context.Context, tip, after string) (bool, error) {
	commits, err := r.vcs.RangeCommits(ctx, tip, after, nil)
	if err != nil || len(commits) == 0 {
		return false, err
	}
	for i, cm := range commits {
		want := tip
		if i+1 < len(commits) {
			want = commits[i+1].ID
		}
		if !slices.Equal(cm.Parents, []string{want}) {
			return false, nil
		}
	}
	return true, nil
}

// handOver returns the commit to hand the provider so that its merge lands exactly
// rd.tree: the head when the provider's own merge already does, else an update commit
// pushed to the change's branch under a lease on the head.
//
// The update commit may differ from the provider's own merge only where no reviewer's
// view changes: in files the plan's base marks generated, or, for a change stacked on
// one that landed as a squash, in exactly what that change landed. The second is
// proven, not assumed: merging the head onto tip from its stack base must give the
// validated tree, so the update commit is tip plus the change's own delta, the diff
// its reviewers approved.
func (r *applyRun) handOver(ctx context.Context, rd *ready) (string, error) {
	c := rd.v.Change
	plain, err := r.vcs.MergeTrees(ctx, TreeMerge{Ours: rd.tip, Theirs: c.Head})
	if err != nil {
		return "", err
	}
	if len(plain.Conflicts) == 0 && plain.Tree == rd.tree {
		return c.Head, nil
	}
	src, err := r.unreviewed(ctx, plain.Tree, rd.tree)
	if err != nil {
		return "", err
	}
	stacked := false
	if len(src) > 0 && c.StackBase != "" {
		own, err := r.vcs.MergeTrees(ctx, TreeMerge{Base: c.StackBase, Ours: rd.tip, Theirs: c.Head})
		if err != nil {
			return "", err
		}
		if len(own.Conflicts) == 0 {
			if src, err = r.unreviewed(ctx, own.Tree, rd.tree); err != nil {
				return "", err
			}
			stacked = len(src) == 0
		}
	}
	if len(src) > 0 {
		return "", &RefusedError{Paths: src, Reason: "validated at `" + short(c.Head) + "`, but merging it the way the host would differs from what was validated in " +
			joinPaths(src) + "; rebase it onto `" + r.plan.Base + "` and queue it again"}
	}
	if c.Branch == "" {
		return "", &RefusedError{Reason: "its merge onto `" + r.plan.Base + "` needs an update commit, and the queue cannot push to its branch. " +
			"Merge `" + r.plan.Base + "` in, regenerate, push, and queue it again."}
	}
	head, err := r.vcs.FindCommit(ctx, c.Head)
	if err != nil {
		return "", err
	}
	parents, msg := []string{c.Head, rd.tip}, "merge "+r.plan.Base+" into #"+c.ID+" and regenerate generated files"
	switch {
	case c.Method == MethodRebase:
		// The provider replays the head's commits and then this one, whose tree is the
		// validated one.
		parents, msg = []string{c.Head}, "regenerate generated files on "+r.plan.Base
	case stacked && r.caps.LinearStacks:
		parents, msg = []string{rd.tip}, "restack #"+c.ID+" onto "+r.plan.Base
	}
	update, err := r.vcs.CommitTree(ctx, TreeCommit{
		CommitMeta: CommitMeta{Message: msg, Author: head.Author, Committer: r.Committer},
		Tree:       rd.tree, Parents: parents,
	})
	if err != nil {
		return "", err
	}
	err = r.vcs.Push(ctx, PushLease{Ref: branchRef(c.Branch), To: update, Expected: c.Head})
	if errors.Is(err, ErrStaleLease) {
		return "", &WaitError{Code: CodeBranchMoved, Reason: "its branch moved or was deleted since validation"}
	}
	return update, err
}

// unreviewed lists the paths a and b differ in that the plan's base does not mark
// generated.
func (r *applyRun) unreviewed(ctx context.Context, a, b string) ([]string, error) {
	diff, err := r.vcs.DiffTrees(ctx, a, b)
	if err != nil {
		return nil, err
	}
	return sources(ctx, r.vcs, r.plan.BaseCommit, diff)
}

// landRun lands a run of stacked changes in one provider call, through its highest green
// member; the members above it are settled one by one. It returns how many changes it
// settled.
func (r *applyRun) landRun(ctx context.Context, run []Change) (int, error) {
	green := 0
	for _, c := range run {
		v, ok := r.got[c.ID]
		if !ok || v.Decision != DecisionMerge || v.BaseCommit != r.plan.BaseCommit || (green > 0 && v.After != run[green-1].ID) {
			break
		}
		green++
	}
	bottom := r.got[run[0].ID]
	if green < 2 || (bottom.After != "" && !r.merged[bottom.After]) {
		return 1, r.settle(ctx, bottom)
	}
	run = run[:green]
	tip, err := r.vcs.FetchRef(ctx, branchRef(r.plan.Base))
	if err != nil {
		return 0, fmt.Errorf("resolve %s: %w", r.plan.Base, err)
	}
	var steps []*ready
	for i, c := range run {
		rd, err := r.check(ctx, r.got[c.ID], tip, bottom.Onto)
		if err != nil {
			return 0, err
		}
		if rd == nil {
			// It waits or was kicked back; what is stacked on it cannot land without it.
			for _, above := range run[i+1:] {
				if err := r.wait(ctx, above, above.Head, CodeParent, "stacked on #"+c.ID+", which did not merge"); err != nil {
					return 0, err
				}
			}
			break
		}
		steps = append(steps, rd)
	}
	atomic := len(steps) > 1
	if atomic {
		if atomic, err = r.ownDeltas(ctx, tip, steps); err != nil {
			return 0, err
		}
	}
	if !atomic {
		// One at a time, each checked again onto what the one beneath it left.
		for _, st := range steps {
			if err := r.settle(ctx, st.v); err != nil {
				return 0, err
			}
		}
		return len(run), nil
	}
	top := steps[len(steps)-1]
	for _, st := range steps {
		if err := r.post(ctx, st.v.Change, st.v.Change.Head, StatePending, "applying candidate "+short(st.v.Candidate)+" in a stack"); err != nil {
			return 0, err
		}
	}
	mergeErr := r.provider.MergeChange(ctx, top.v.Change, MergeRequest{Commit: top.v.Change.Head, Message: top.v.Message, Through: run[0].ID})
	after, err := r.vcs.FetchRef(ctx, branchRef(r.plan.Base))
	if err != nil {
		return 0, fmt.Errorf("resolve %s after merging a stack: %w", r.plan.Base, err)
	}
	if mergeErr != nil && after == tip {
		for _, st := range steps {
			if err := r.wait(ctx, st.v.Change, st.v.Change.Head, CodeHostRefused, "the host refused the stack merge: "+mergeErr.Error()); err != nil {
				return 0, err
			}
		}
		return len(steps), nil
	}
	landed, err := r.stackLanded(ctx, tip, after, steps)
	if err != nil {
		return 0, err
	}
	for _, st := range steps[:landed] {
		r.merged[st.v.Change.ID] = true
		r.Events.Emit(Event{Kind: EventMerged, Change: st.v.Change.ID, Commit: st.v.Change.Head})
		if err := r.post(ctx, st.v.Change, st.v.Change.Head, StateSuccess, "merged in a stack as "+short(after)); err != nil {
			r.Events.Emit(Event{Kind: EventNotice, Change: st.v.Change.ID, Reason: err.Error()})
		}
	}
	if landed < len(steps) {
		return 0, fmt.Errorf("the stack through %s landed %d of %d changes; stopping: %v", top.v.Change.Label(), landed, len(steps), mergeErr)
	}
	return len(steps), nil
}

// ownDeltas reports whether what a provider landing a stack atomically produces is each
// step's validated tree: the bottom merged from its natural merge base, even when the
// change it is stacked on landed as a squash, and each member above as its own delta
// from the member below. When it does not (a candidate regenerated a file, or the bottom
// would bring back what the change beneath it deleted), the run lands one change at a
// time with update commits instead.
func (r *applyRun) ownDeltas(ctx context.Context, tip string, steps []*ready) (bool, error) {
	below := tip
	for i, st := range steps {
		if i > 0 {
			var err error
			if below, err = r.vcs.CommitTree(ctx, TreeCommit{CommitMeta: queueMeta("expected"), Tree: steps[i-1].tree, Parents: []string{below}}); err != nil {
				return false, err
			}
		}
		base := ""
		if i > 0 {
			base = st.v.Change.StackBase
		}
		m, err := r.vcs.MergeTrees(ctx, TreeMerge{Base: base, Ours: below, Theirs: st.v.Change.Head})
		if err != nil {
			return false, err
		}
		if len(m.Conflicts) > 0 || m.Tree != st.tree {
			return false, nil
		}
	}
	return true, nil
}

// stackLanded counts the steps landed between tip and after, each checked against its
// validated tree and its merge method's shape, and errors when the base holds anything
// else.
func (r *applyRun) stackLanded(ctx context.Context, tip, after string, steps []*ready) (int, error) {
	commits, err := r.vcs.RangeCommits(ctx, tip, after, nil)
	if err != nil {
		return 0, err
	}
	top := steps[len(steps)-1].v.Change
	if steps[0].v.Method == MethodMerge {
		// One merge commit for the whole run, so only the top's tree is checked and the
		// members' own commits are its second parent's history.
		if err := r.landedAs(ctx, top, tip, after, top.Head, steps[len(steps)-1].tree, MethodMerge); err != nil {
			return 0, err
		}
		return len(steps), nil
	}
	if len(commits) > len(steps) {
		return 0, fmt.Errorf("the stack through %s left %d commits on %s for %d changes; stopping", top.Label(), len(commits), r.plan.Base, len(steps))
	}
	below := tip
	for i := range commits {
		cm := commits[len(commits)-1-i]
		st := steps[i]
		if err := r.landedAs(ctx, st.v.Change, below, cm.ID, st.v.Change.Head, st.tree, MethodSquash); err != nil {
			return 0, err
		}
		below = cm.ID
	}
	return len(commits), nil
}

func refusal(reason string, paths []string) Kick {
	return Kick{Code: CodeRefused, Report: "The merge queue validated this change but cannot merge it: " + reason + "\n", Paths: paths}
}

func (r *applyRun) kick(ctx context.Context, c Change, commit string, k Kick) error {
	r.Events.Emit(Event{Kind: EventKicked, Change: c.ID, Code: k.Code, Reason: firstLine(k.Report)})
	if r.DryRun {
		return nil
	}
	if err := r.post(ctx, c, commit, StateFailure, "kicked back; see the comment"); err != nil {
		return err
	}
	if err := r.provider.KickBack(ctx, c, commit, k); err != nil {
		return fmt.Errorf("kick back %s: %w", c.Label(), err)
	}
	return nil
}

func (r *applyRun) wait(ctx context.Context, c Change, commit string, code Code, reason string) error {
	r.Events.Emit(Event{Kind: EventWaiting, Change: c.ID, Code: code, Reason: reason})
	if r.DryRun {
		return nil
	}
	return r.post(ctx, c, commit, StatePending, "waiting: "+reason)
}

func (r *applyRun) post(ctx context.Context, c Change, commit string, state CommitState, desc string) error {
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
