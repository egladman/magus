package job

import (
	"fmt"
	"slices"
	"strings"

	qtypes "github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/libs/safetyscissors/job"
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
	Fetch   *InflightFetch
	Changes qtypes.Changes
	// Plan is the queue's newest plan for the base, nil when none was recorded.
	Plan *qtypes.Plan
	// Branches maps a job's checkout root to the branch that checkout is on. A root the
	// map lacks joins no change.
	Branches map[string]string
	Me       Identity
}

// InflightList is a job list joined to the open changes against the queue's base: what
// `magus ls jobs` serves. The changes are magus's merge queue and so not the job list's own.
type InflightList struct {
	job.List `yaml:",inline"`
	// Changes are the open changes against the queue's base, joined to the jobs whose
	// checkouts are on their branches, in the order the provider listed them. Read from
	// the snapshot `magus queue ls` leaves; a list never fetches them.
	Changes []InflightChange `json:"changes,omitempty" yaml:"changes,omitempty"`
	// Fetched is when and where that snapshot was read, nil when none was.
	Fetched *InflightFetch `json:"fetched,omitzero" yaml:"fetched,omitempty"`
	// Unproposed are the caller's live jobs whose checkout is on a branch no change
	// carries: work in flight that has no change yet.
	Unproposed []string `json:"unproposed,omitempty" yaml:"unproposed,omitempty"`
}

// InflightChange is one open change as a person reads it: the provider's record joined to
// the jobs working on its branch and to the queue's newest plan for it. The queue's own
// Change is the record it acts on; this one is what it is waiting on and whose turn it is.
type InflightChange struct {
	ID     string `json:"id"               yaml:"id"`
	Title  string `json:"title,omitempty"  yaml:"title,omitempty"`
	Author string `json:"author,omitempty" yaml:"author,omitempty"`
	Head   string `json:"head"             yaml:"head"`
	Branch string `json:"branch,omitempty" yaml:"branch,omitempty"`
	Base   string `json:"base,omitempty"   yaml:"base,omitempty"`
	Fork   bool   `json:"fork"   yaml:"fork,omitempty"`
	// Jobs are the ids of the jobs whose checkout is on Branch, in store order.
	Jobs []string `json:"jobs,omitempty" yaml:"jobs,omitempty"`
	// Intent is the merge method the change is queued with, empty when it carries no
	// merge intent.
	Intent string `json:"intent,omitempty" yaml:"intent,omitempty"`
	// Mark is the queue's mark the provider shows on a change carrying no intent:
	// queued, kicked_back or needs_regeneration.
	Mark string `json:"mark,omitempty" yaml:"mark,omitempty"`
	// Decision, Code and Reason are the plan's verdict on this head, empty when it
	// settled none.
	Decision string `json:"decision,omitempty" yaml:"decision,omitempty"`
	Code     string `json:"code,omitempty"     yaml:"code,omitempty"`
	Reason   string `json:"reason,omitempty"   yaml:"reason,omitempty"`
	// Partition and Position place the change in the plan, both counted from 1; zero
	// when the plan admitted no change at this head. Below is the change it is stacked on.
	Partition int    `json:"partition" yaml:"partition,omitempty"`
	Position  int    `json:"position"  yaml:"position,omitempty"`
	Below     string `json:"below,omitempty"     yaml:"below,omitempty"`
	// Mine is whether the caller is on the change: a job of the caller's job tree works
	// on its branch, or its author is the caller's forge login.
	Mine bool `json:"mine" yaml:"mine,omitempty"`
	// Attention is whose turn the change is. See [InflightAttention].
	Attention InflightAttention `json:"attention" yaml:"attention"`
	// Neighbours are the other in-flight changes this one touches, on the caller's
	// changes only, strongest evidence first.
	Neighbours []InflightNeighbour `json:"neighbours,omitempty" yaml:"neighbours,omitempty"`
}

// InflightAttention is whose turn an in-flight change is, derived from its facts and
// never written by hand.
type InflightAttention string

const (
	// AttentionAuthor: the queue kicked it back, so its author acts next.
	AttentionAuthor InflightAttention = "author"
	// AttentionReview: it waits for an approval at its head.
	AttentionReview InflightAttention = "review"
	// AttentionQueue: it carries merge intent and the queue acts next.
	AttentionQueue InflightAttention = "queue"
	// AttentionNone: it carries no merge intent and nothing waits on anyone.
	AttentionNone InflightAttention = "none"
)

// InflightNeighbour is another in-flight change a change touches, by the strongest
// evidence that holds: merge_conflict, then declaration, file, affected.
type InflightNeighbour struct {
	// ID is the other change, or a job id when that work has no change yet.
	ID           string   `json:"id"                     yaml:"id"`
	Evidence     string   `json:"evidence"               yaml:"evidence"`
	Paths        []string `json:"paths,omitempty"        yaml:"paths,omitempty"`
	Declarations []string `json:"declarations,omitempty" yaml:"declarations,omitempty"`
	Units        []string `json:"units,omitempty"        yaml:"units,omitempty"`
	// Base is the merge base the comparison ran on.
	Base string `json:"base,omitempty" yaml:"base,omitempty"`
}

// InflightFetch is the network read an in-flight list rests on.
type InflightFetch struct {
	// Provider is the queue provider that answered, as `magus queue ls --provider` named it.
	Provider string `json:"provider"         yaml:"provider"`
	Host     string `json:"host,omitempty"   yaml:"host,omitempty"`
	Base     string `json:"base"             yaml:"base"`
	// Tip is the base commit the plan was built on, empty when no plan was recorded.
	Tip string `json:"tip,omitempty" yaml:"tip,omitempty"`
	// At is unix seconds, and ElapsedMS how long the provider took to answer.
	At        int64 `json:"at"         yaml:"at"`
	ElapsedMS int64 `json:"elapsed_ms" yaml:"elapsed_ms"`
}

// Inflight joins jobs to in: its Changes, Fetched and Unproposed. It reads nothing and
// calls nothing, so one store and one snapshot always render the same list.
func Inflight(jobs job.List, in InflightInput) InflightList {
	list := InflightList{List: jobs}
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

	add := func(ch InflightChange) {
		if ch.Branch != "" {
			ch.Jobs = slices.Clone(byBranch[ch.Branch])
		}
		key := fmt.Sprintf("%s@%s", ch.ID, ch.Head)
		if p, ok := places[key]; ok {
			ch.Partition, ch.Position, ch.Below = p.partition, p.position, p.below
		}
		if v, ok := verdicts[key]; ok {
			ch.Decision, ch.Code, ch.Reason = v.Decision.String(), string(v.Code), v.Reason
		}
		ch.Mine = (in.Me.Login != "" && ch.Author == in.Me.Login) ||
			slices.ContainsFunc(ch.Jobs, func(id string) bool { return tree[id] })
		ch.Attention = changeAttention(ch)
		list.Changes = append(list.Changes, ch)
	}
	for _, c := range in.Changes.Changes {
		add(InflightChange{
			ID: c.ID, Title: c.Title, Author: c.Author, Head: c.Head, Branch: c.Branch, Base: c.Base, Fork: c.Fork,
			Intent: string(c.Method),
		})
	}
	for _, u := range in.Changes.Unqueued {
		// The provider lists every open change it left out, whatever it targets.
		if u.Base != "" && u.Base != in.Changes.Base {
			continue
		}
		add(InflightChange{
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
func changeAttention(ch InflightChange) InflightAttention {
	switch {
	case ch.Mark == string(qtypes.MarkKickedBack), ch.Mark == string(qtypes.MarkNeedsRegeneration),
		ch.Decision == qtypes.DecisionKick.String():
		return AttentionAuthor
	case ch.Code == string(qtypes.CodeWaitNotApproved):
		return AttentionReview
	case ch.Intent != "":
		return AttentionQueue
	}
	return AttentionNone
}

// InflightDetail is why a change is where it is: its place in the plan, and
// the verdict's code and reason or the mark it shows. It names the partition
// only when partitions exceeds one, and is "" with nothing to say.
func InflightDetail(c InflightChange, partitions int) string {
	var parts []string
	if c.Position > 0 {
		place := fmt.Sprintf("pos %d", c.Position)
		if partitions > 1 {
			place = fmt.Sprintf("partition %d pos %d", c.Partition, c.Position)
		}
		parts = append(parts, place)
	}
	switch {
	case c.Code != "" && c.Reason != "":
		parts = append(parts, fmt.Sprintf("%s: %s", c.Code, c.Reason))
	case c.Code != "":
		parts = append(parts, c.Code)
	case c.Mark != "":
		parts = append(parts, strings.ReplaceAll(c.Mark, "_", " "))
	}
	return strings.Join(parts, "  ")
}

// jobTree is the ids of lease's whole tree: its root ancestor and every row below it.
func jobTree(rows []job.Job, lease string) map[string]bool {
	if lease == "" {
		return nil
	}
	root := lease
	if up := job.Ancestors(rows, lease); len(up) > 0 {
		root = up[len(up)-1].ID
	}
	tree := map[string]bool{root: true}
	for _, row := range job.Descendants(rows, root) {
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
			places[fmt.Sprintf("%s@%s", c.ID, c.Head)] = planPlace{partition: i + 1, position: j + 1, below: c.Below}
		}
	}
	for _, v := range plan.Verdicts {
		verdicts[fmt.Sprintf("%s@%s", v.Change.ID, v.Change.Head)] = v
	}
	return places, verdicts
}
