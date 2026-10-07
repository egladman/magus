package job

import (
	"slices"

	qtypes "github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/types"
)

// Identity is who "mine" means for an in-flight list.
type Identity struct {
	// Lease is the job the caller acts under. Every job of its tree, from the root it was
	// forked under down, is the caller's.
	Lease string
	// Login is the caller's forge login, "" when nothing in this process has named it.
	Login string
}

// Known reports whether anything names the caller. A list for nobody in particular has
// no changes of its own, so a renderer shows everyone's.
func (me Identity) Known() bool { return me.Lease != "" || me.Login != "" }

// InflightInput is what [Inflight] joins a job list to.
type InflightInput struct {
	// Fetch is the read the changes came from, nil when the queue was never read.
	Fetch   *types.InflightFetch
	Changes qtypes.Changes
	// Plan is the queue's newest plan for the base, nil when none was recorded.
	Plan *qtypes.Plan
	// Branches maps a job's checkout root to the branch that checkout is on. A root the
	// map lacks joins no change.
	Branches map[string]string
	Me       Identity
}

// Inflight fills list's Changes, Fetched and Unproposed from in. It reads nothing and
// calls nothing, so one store and one snapshot always render the same list.
func Inflight(list types.JobList, in InflightInput) types.JobList {
	list.Changes, list.Fetched, list.Unproposed = nil, nil, nil
	if in.Fetch == nil {
		return list
	}
	fetch := *in.Fetch
	plan := in.Plan
	if plan != nil && plan.Base != in.Changes.Base {
		plan = nil
	}
	if plan != nil {
		fetch.Tip = plan.BaseCommit
	}
	list.Fetched = &fetch

	byBranch := map[string][]string{}
	for _, row := range list.Jobs {
		if b := in.Branches[row.CheckoutRoot]; row.CheckoutRoot != "" && b != "" {
			byBranch[b] = append(byBranch[b], row.ID)
		}
	}
	tree := jobTree(list.Jobs, in.Me.Lease)
	places, verdicts := planFacts(plan)

	add := func(ch types.InflightChange) {
		if ch.Branch != "" {
			ch.Jobs = slices.Clone(byBranch[ch.Branch])
		}
		key := ch.ID + "@" + ch.Head
		if p, ok := places[key]; ok {
			ch.Partition, ch.Position, ch.Below = p.partition, p.position, p.below
		}
		if v, ok := verdicts[key]; ok {
			ch.Decision, ch.Code, ch.Reason = string(v.Decision), string(v.Code), v.Reason
		}
		ch.Mine = (in.Me.Login != "" && ch.Author == in.Me.Login) ||
			slices.ContainsFunc(ch.Jobs, func(id string) bool { return tree[id] })
		ch.Attention = changeAttention(ch)
		list.Changes = append(list.Changes, ch)
	}
	for _, c := range in.Changes.Changes {
		add(types.InflightChange{
			ID: c.ID, Title: c.Title, Author: c.Author, Head: c.Head, Branch: c.Branch, Base: c.Base, Fork: c.Fork,
			Intent: string(c.Method),
		})
	}
	for _, u := range in.Changes.Unqueued {
		// The provider lists every open change it left out, whatever it targets.
		if u.Base != "" && u.Base != in.Changes.Base {
			continue
		}
		add(types.InflightChange{
			ID: u.ID, Title: u.Title, Author: u.Author, Head: u.Head, Branch: u.Branch, Base: u.Base,
			Mark: string(u.Mark),
		})
	}

	carried := map[string]bool{}
	for _, ch := range list.Changes {
		if ch.Branch != "" {
			carried[ch.Branch] = true
		}
	}
	for _, row := range list.Jobs {
		if tree[row.ID] && row.State.Live() && row.CheckoutRoot != "" && !carried[in.Branches[row.CheckoutRoot]] {
			list.Unproposed = append(list.Unproposed, row.ID)
		}
	}
	return list
}

// changeAttention is whose turn ch is. A kick-back outranks intent: a change still carrying
// intent after the queue kicked it is waiting on its author, not on the queue.
func changeAttention(ch types.InflightChange) types.InflightAttention {
	switch {
	case ch.Mark == string(qtypes.MarkKickedBack), ch.Mark == string(qtypes.MarkNeedsRegeneration),
		ch.Decision == string(qtypes.DecisionKick):
		return types.AttentionAuthor
	case ch.Code == string(qtypes.CodeWaitNotApproved):
		return types.AttentionReview
	case ch.Intent != "":
		return types.AttentionQueue
	}
	return types.AttentionNone
}

// jobTree is the ids of lease's whole tree: its root ancestor and every row below it.
func jobTree(rows []types.Job, lease string) map[string]bool {
	if lease == "" {
		return nil
	}
	root := lease
	if up := types.JobAncestors(rows, lease); len(up) > 0 {
		root = up[len(up)-1].ID
	}
	tree := map[string]bool{root: true}
	for _, row := range types.JobDescendants(rows, root) {
		tree[row.ID] = true
	}
	return tree
}

type planPlace struct {
	partition, position int
	below               string
}

// planFacts indexes plan by id and head: a change whose head moved since planning is not
// the change the plan placed.
func planFacts(plan *qtypes.Plan) (map[string]planPlace, map[string]qtypes.Verdict) {
	places, verdicts := map[string]planPlace{}, map[string]qtypes.Verdict{}
	if plan == nil {
		return places, verdicts
	}
	for i, part := range plan.Partitions {
		for j, c := range part {
			places[c.ID+"@"+c.Head] = planPlace{partition: i + 1, position: j + 1, below: c.Below}
		}
	}
	for _, v := range plan.Verdicts {
		verdicts[v.Change.ID+"@"+v.Change.Head] = v
	}
	return places, verdicts
}
