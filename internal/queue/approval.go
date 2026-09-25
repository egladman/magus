package queue

import (
	"context"
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/queue/types"
	magustypes "github.com/egladman/magus/types"
)

// approvalResult is an approval with what it was asked about.
type approvalResult struct {
	types.Approval
	reviewed string              // the commit asked about
	owed     []regenerationProof // what reviewed covers only once the base's regeneration proves it
	carried  string              // the older commit an approval was carried over from
}

// approval asks prov for c's approval at the commit a review of c.Head covers. stackBase
// is c's stack base once the change beneath it merged, else "". When the approval stands
// only at an older commit, it carries over if c.Head is that commit rebased with nothing
// else changed. A provider that reports no head, base or method is broken: without them
// the queue cannot tell what it would merge.
func approval(ctx context.Context, prov types.Provider, v types.ReadVCS, f types.BuildFacts, cl Clone, tip string, c types.Change, stackBase string, refs []stackRef) (approvalResult, error) {
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
	ok, err := rebasedFrom(ctx, v, cl, tip, a.ApprovedCommit, reviewed, c.StackBase, refs)
	if err != nil {
		return approvalResult{}, fmt.Errorf("compare %s with its approved %s: %w", c.Label(), short(a.ApprovedCommit), err)
	}
	if ok {
		res.Approved, res.Reason, res.carried = true, "", a.ApprovedCommit
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

// rebasedFrom reports whether now is old moved onto a new base with its own delta
// unchanged: it reads what [carryBase] decides over, and when that finds old's delta,
// replays it onto now's base.
func rebasedFrom(ctx context.Context, v types.ReadVCS, cl Clone, tip, old, now, stackBase string, refs []stackRef) (bool, error) {
	if err := v.FetchCommit(ctx, cl.Root, cl.Remote, old); err != nil {
		return false, err
	}
	f := carryFacts{old: old}
	var err error
	if f.commits, err = v.RangeCommits(ctx, cl.Root, tip, old, nil); err != nil {
		return false, err
	}
	for _, r := range refs {
		cr := carryRef{stackRef: r}
		if !r.unqueued && r.head != now {
			if err := v.FetchCommit(ctx, cl.Root, cl.Remote, r.head); err != nil {
				return false, err
			}
			if cr.own, err = ownCommits(ctx, v, cl.Root, tip, r.head); err != nil {
				return false, err
			}
		}
		f.refs = append(f.refs, cr)
	}
	oldBase, ok := carryBase(f)
	if !ok {
		return false, nil
	}
	newBase := stackBase
	if newBase == "" {
		if newBase, err = forkPoint(ctx, v, cl.Root, tip, now); err != nil {
			return false, err
		}
	}
	return trivialRebase(ctx, v, cl.Root, oldBase, old, newBase, now)
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

func carriedNotice(a approvalResult) string {
	return "head " + short(a.reviewed) + " is " + short(a.carried) + " rebased with its diff unchanged, so its approval carried over"
}
