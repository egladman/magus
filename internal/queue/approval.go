package queue

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/queue/types"
	magustypes "github.com/egladman/magus/types"
)

// approvalResult is an approval with what it was asked about.
type approvalResult struct {
	types.Approval
	reviewed string              // the commit asked about
	owed     []regenerationProof // what reviewed covers only once the base's regeneration proves it
	// carry is the verdict on carrying an approval over from an older commit, nil when
	// none was asked for.
	carry *types.CarryVerdict
}

// approval asks prov for c's approval at the commit a review of c.Head covers. stackBase
// is c's stack base once the change beneath it merged, else "". When the approval stands
// only at an older commit, it carries over if c.Head is that commit rebased with nothing
// changed beyond what policy allows. A provider that reports no head, base or method is
// broken: without them the queue cannot tell what it would merge.
func approval(ctx context.Context, prov types.Provider, v types.ReadVCS, f types.BuildFacts, cl Clone, tip string, c types.Change, stackBase string, refs []stackRef, policy types.CarryPolicy) (approvalResult, error) {
	reviewed, owed, err := reviewTarget(ctx, v, f, cl.Root, tip, c.Head, stackBase)
	if err != nil {
		return approvalResult{}, fmt.Errorf("review target of %s: %w", c.Label(), err)
	}
	a, err := prov.ApprovalAt(ctx, c, reviewed)
	if err != nil {
		return approvalResult{}, fmt.Errorf("approval of %s: %w", c.Label(), err)
	}
	switch {
	case a.Head == "":
		return approvalResult{}, fmt.Errorf("approval of %s: provider reported no head", c.Label())
	case a.Base == "" || !a.Method.Valid():
		return approvalResult{}, fmt.Errorf("approval of %s: provider reported base %q and merge method %q, both required", c.Label(), a.Base, a.Method)
	}
	res := approvalResult{Approval: a, reviewed: reviewed, owed: owed}
	if a.Approved || a.ApprovedCommit == "" || a.ApprovedCommit == reviewed || a.Head != c.Head {
		return res, nil
	}
	if !types.IsObjectID(a.ApprovedCommit) {
		return approvalResult{}, fmt.Errorf("approval of %s: provider reported approvals at %q, not a commit id", c.Label(), a.ApprovedCommit)
	}
	verdict, err := carryApproval(ctx, v, f, cl, tip, a.ApprovedCommit, reviewed, c.StackBase, refs, policy)
	if err != nil {
		return approvalResult{}, fmt.Errorf("compare %s with its approved %s: %w", c.Label(), short(a.ApprovedCommit), err)
	}
	res.carry = &verdict
	switch {
	case verdict.Carry:
		res.Approved, res.Reason = true, ""
	case res.Reason == "":
		res.Reason = verdict.Reason
	default:
		res.Reason += "; " + verdict.Reason
	}
	return res, nil
}

// admission is the wait or kick planning decides from c's approval, or nil when c may
// be admitted. A head that moved while listing becomes c's head.
func admission(c *types.Change, a approvalResult, caps types.Capabilities) *types.Verdict {
	switch {
	case a.Head != c.Head:
		c.Head = a.Head
		return decided(*c, types.DecisionWait, types.CodeWaitHeadMoved, "head moved to "+short(a.Head)+" while listing; retried next run", "")
	case !a.Queued:
		return decided(*c, types.DecisionWait, types.CodeWaitWithdrawn, "its merge intent was withdrawn while listing", "")
	case !a.Approved:
		return decided(*c, types.DecisionWait, types.CodeWaitNotApproved, "not approved at "+short(a.reviewed)+reasonSuffix(a.Reason), "")
	case !caps.Allows(c.Method):
		return refused(*c, "the repository does not allow the "+string(c.Method)+" merge method; pick one it does")
	}
	return nil
}

// CarryQuery names one approval for [CarryApproval] to classify.
type CarryQuery struct {
	// Approved is the commit the approval was given at.
	Approved string
	// Now is the commit the approval has to cover: a change's head, or the commit a
	// review of the head covers.
	Now string
	// StackBase is the head of the change Now is stacked on, where Now's own delta
	// starts; empty measures it from where Now leaves the base's history.
	StackBase string
	// Listing is the provider's listing Now's change came from. Its changes, merged
	// changes and unqueued changes decide where Approved's own delta starts.
	Listing types.Changes
}

// CarryApproval reports whether the approval given at q.Approved still covers q.Now
// under policy. It is the classifier the queue admits changes by, so a caller that
// dismisses the reviews it would not count agrees with the merge gate. It fetches
// q.Listing.Base and q.Approved from cl's remote, and runs none of either commit's code.
func CarryApproval(ctx context.Context, v types.ReadVCS, f types.BuildFacts, cl Clone, q CarryQuery, policy types.CarryPolicy) (types.CarryVerdict, error) {
	if !types.IsObjectID(q.Approved) || !types.IsObjectID(q.Now) {
		return types.CarryVerdict{}, fmt.Errorf("carry an approval from %q to %q: both must be commit ids", q.Approved, q.Now)
	}
	tip, err := fetchBase(ctx, v, cl, q.Listing.Base)
	if err != nil {
		return types.CarryVerdict{}, err
	}
	return carryApproval(ctx, v, f, cl, tip, q.Approved, q.Now, q.StackBase, planRefs(q.Listing), policy)
}

// carryApproval replays old's own delta onto now's base and classifies, path by path,
// what now holds beyond that replay. A delta [replayBases] cannot tell apart from another
// change's, or one that conflicts on the new base, is code: what the approval saw is
// then not a diff the head still has.
func carryApproval(ctx context.Context, v types.ReadVCS, f types.BuildFacts, cl Clone, tip, old, now, stackBase string, refs []stackRef, policy types.CarryPolicy) (types.CarryVerdict, error) {
	verdict := types.CarryVerdict{From: old, Head: now, Tier: types.CarryCode}
	oldBase, newBase, why, err := replayBases(ctx, v, cl, tip, old, now, stackBase, refs)
	if err != nil || why != "" {
		verdict.Reason = why
		return verdict, err
	}
	r, err := v.MergeTrees(ctx, cl.Root, magustypes.TreeMerge{Base: oldBase, Ours: newBase, Theirs: old})
	if err != nil {
		return verdict, err
	}
	if len(r.Conflicts) > 0 {
		for _, c := range r.Conflicts {
			verdict.Refused = append(verdict.Refused, c.Path)
		}
		verdict.Reason = "the approved " + short(old) + "'s diff conflicts with " + short(newBase) + " in " + strings.Join(verdict.Refused, ", ")
		return verdict, nil
	}
	tree, err := v.TreeID(ctx, cl.Root, now)
	if err != nil {
		return verdict, err
	}
	if r.Tree != tree {
		changed, err := v.DiffTrees(ctx, cl.Root, r.Tree, tree)
		if err != nil {
			return verdict, err
		}
		if len(changed) == 0 {
			verdict.Reason = "head " + short(now) + "'s tree differs from the approved " + short(old) + " replayed, in no path the VCS names"
			return verdict, nil
		}
		for _, p := range changed {
			c, err := classifyChanged(ctx, v, f, cl.Root, r.Tree, now, p)
			if err != nil {
				return verdict, err
			}
			verdict.Changed = append(verdict.Changed, c)
		}
	}
	verdict.Decide(policy)
	return verdict, nil
}

// classifyChanged asks the build tool for the tier of path's edit from the replayed tree
// to now. A tier the build tool has no business answering for a path is code.
func classifyChanged(ctx context.Context, v types.ReadVCS, f types.BuildFacts, root, replayed, now, path string) (types.ClassifiedPath, error) {
	c, err := f.ClassifyEdit(ctx, path, readSide(ctx, v, root, replayed, path), readSide(ctx, v, root, now, path))
	if err != nil {
		return types.ClassifiedPath{}, fmt.Errorf("classify %s: %w", path, err)
	}
	c.Path = path
	switch c.Tier {
	case types.CarryGenerated, types.CarryProse, types.CarryCommentOnly, types.CarryCode:
	default:
		c.Why = fmt.Sprintf("the build tool answered tier %q for a path", c.Tier)
		c.Tier = types.CarryCode
	}
	return c, nil
}

// readSide is path's content at rev, nil where rev does not hold it. An unreadable path
// reads as absent, which no comment-only edit is.
func readSide(ctx context.Context, v types.ReadVCS, root, rev, path string) []byte {
	content, err := v.ReadFileAt(ctx, root, rev, path)
	if err != nil {
		return nil
	}
	return []byte(content)
}

// replayBases returns where old's own delta starts and where now's does, reading what
// [carryBase] decides over. why says, when non-empty, why old's delta cannot be replayed.
func replayBases(ctx context.Context, v types.ReadVCS, cl Clone, tip, old, now, stackBase string, refs []stackRef) (oldBase, newBase, why string, err error) {
	if err := v.FetchCommit(ctx, cl.Root, cl.Remote, old); err != nil {
		return "", "", "", err
	}
	f := carryFacts{old: old}
	if f.commits, err = v.RangeCommits(ctx, cl.Root, tip, old, nil); err != nil {
		return "", "", "", err
	}
	for _, r := range refs {
		cr := carryRef{stackRef: r}
		if !r.unqueued && r.head != now {
			if err := v.FetchCommit(ctx, cl.Root, cl.Remote, r.head); err != nil {
				return "", "", "", err
			}
			if cr.own, err = ownCommits(ctx, v, cl.Root, tip, r.head); err != nil {
				return "", "", "", err
			}
		}
		f.refs = append(f.refs, cr)
	}
	oldBase, ok := carryBase(f)
	if !ok {
		return "", "", "the approved " + short(old) + "'s own diff cannot be told apart from another change's", nil
	}
	newBase = stackBase
	if newBase == "" {
		if newBase, err = forkPoint(ctx, v, cl.Root, tip, now); err != nil {
			return "", "", "", err
		}
	}
	if oldBase == "" || newBase == "" {
		return "", "", "the approved " + short(old) + " or head " + short(now) + " merged the base's history in more than once, so no replay moves its diff", nil
	}
	return oldBase, newBase, "", nil
}

// carryFacts is what carrying an approval over from old reads.
type carryFacts struct {
	old     string
	commits []magustypes.Commit // old's own commits, those the base does not carry, with their parents
	refs    []carryRef
}

// carryRef is a change old may carry the head of, and its own commits; nil for an
// unqueued change, whose head alone matters, and for the change being approved.
type carryRef struct {
	stackRef
	own map[string]bool
}

// carryBase returns where old's own delta starts, which is what its reviewers saw: the
// head of the change it is stacked on, else where it left the base's history. It refuses
// whenever that delta cannot be told apart from another change's: old carries an
// unqueued change's head, carries two changes stacked on neither, has commits beneath
// its stack base's head that are not that change's, or shares a commit with another
// change it does not carry, since the approval was then given on a diff that excluded
// what the rebase now includes.
func carryBase(f carryFacts) (string, bool) {
	own := make(map[string]bool, len(f.commits))
	parents := make(map[string][]string, len(f.commits))
	for _, c := range f.commits {
		own[c.ID] = true
		parents[c.ID] = c.Parents
	}
	var carried []carryRef
	for _, r := range f.refs {
		if r.head != f.old && own[r.head] {
			if r.unqueued {
				return "", false
			}
			carried = append(carried, r)
		}
	}
	nearest, ok := nearestRef(carried)
	if !ok {
		return "", false
	}
	delta := own
	base := nearest.head
	if base != "" {
		delta = map[string]bool{}
		for id := range own {
			if !nearest.own[id] && id != base {
				delta[id] = true
			}
		}
		if !sitsOn(delta, own, parents, base) {
			return "", false
		}
	} else {
		base = forkOf(f.old, f.commits)
	}
	for _, r := range f.refs {
		if r.unqueued || r.id == nearest.id || own[r.head] {
			continue
		}
		for id := range delta {
			if r.own[id] {
				return "", false
			}
		}
	}
	return base, true
}

// nearestRef is the one of refs whose head carries every other's, the zero carryRef when
// refs is empty, and false when two of them are stacked on neither.
func nearestRef(refs []carryRef) (carryRef, bool) {
	if len(refs) == 0 {
		return carryRef{}, true
	}
	for _, n := range refs {
		if !slices.ContainsFunc(refs, func(o carryRef) bool { return o.id != n.id && o.head != n.head && !n.own[o.head] }) {
			return n, true
		}
	}
	return carryRef{}, false
}

// sitsOn reports whether every commit of delta has only parents in delta, base, or
// outside own (the base branch's history): delta forked from base and nowhere else.
func sitsOn(delta, own map[string]bool, parents map[string][]string, base string) bool {
	for id := range delta {
		for _, p := range parents[id] {
			if !delta[p] && p != base && own[p] {
				return false
			}
		}
	}
	return true
}

// carriedNotice is the notice an approval carried over from an older commit writes, ""
// when none did.
func carriedNotice(a approvalResult) string {
	if a.carry == nil || !a.carry.Carry {
		return ""
	}
	return a.carry.Reason + ", so its approval carried over"
}
