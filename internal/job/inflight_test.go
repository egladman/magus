package job

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	qtypes "github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/types"
)

func inflightRows() []types.Job {
	return []types.Job{
		{ID: "fleet", State: types.StateRunning},
		{ID: "fleet/kick", Parent: "fleet", State: types.StatePass, CheckoutRoot: "/w/kick"},
		{ID: "fleet/queued", Parent: "fleet", State: types.StateRunning, CheckoutRoot: "/w/queued"},
		{ID: "fleet/nopr", Parent: "fleet", State: types.StateRunning, CheckoutRoot: "/w/nopr"},
		{ID: "other", State: types.StateRunning},
		{ID: "other/work", Parent: "other", State: types.StateRunning, CheckoutRoot: "/w/theirs"},
	}
}

func inflightInput(me Identity) InflightInput {
	queued := qtypes.Change{ID: "2", Head: "h2", Branch: "queued", Base: "main", Title: "queued", Author: "eli", Method: qtypes.MethodSquash}
	theirs := qtypes.Change{ID: "3", Head: "h3", Branch: "theirs", Base: "main", Author: "ann", Method: qtypes.MethodRebase}
	unapproved := qtypes.Change{ID: "4", Head: "h4", Base: "main", Author: "eli", Method: qtypes.MethodSquash}
	return InflightInput{
		Fetch: &types.InflightFetch{Provider: "github", Host: "github.com", Base: "main", At: 100, ElapsedMS: 7},
		Changes: qtypes.Changes{
			Base:    "main",
			Changes: []qtypes.Change{theirs, queued, unapproved},
			Unqueued: []qtypes.UnqueuedChange{
				{ID: "1", Head: "h1", Branch: "kick", Base: "main", Mark: qtypes.MarkKickedBack},
				{ID: "5", Head: "h5", Branch: "elsewhere", Base: "release"},
				{ID: "6", Head: "h6", Base: "main", Author: "eli"},
			},
		},
		Plan: &qtypes.Plan{
			Base: "main", BaseCommit: "tip",
			Partitions: [][]qtypes.Change{{queued, theirs}},
			Verdicts:   []qtypes.Verdict{{Change: unapproved, Decision: qtypes.DecisionWait, Code: qtypes.CodeWaitNotApproved, Reason: "no approval"}},
		},
		Branches: map[string]string{"/w/kick": "kick", "/w/queued": "queued", "/w/nopr": "nopr", "/w/theirs": "theirs"},
		Me:       me,
	}
}

func TestInflightJoinsChangesToJobsAndThePlan(t *testing.T) {
	t.Parallel()
	list := Inflight(types.JobList{Jobs: inflightRows()}, inflightInput(Identity{Lease: "fleet/queued", Login: "eli"}))

	assert.Equal(t, &types.InflightFetch{Provider: "github", Host: "github.com", Base: "main", Tip: "tip", At: 100, ElapsedMS: 7}, list.Fetched)
	assert.Equal(t, []types.InflightChange{
		{ID: "3", Head: "h3", Branch: "theirs", Base: "main", Author: "ann", Jobs: []string{"other/work"}, Intent: "rebase",
			Partition: 1, Position: 2, Attention: types.AttentionQueue},
		{ID: "2", Title: "queued", Head: "h2", Branch: "queued", Base: "main", Author: "eli", Jobs: []string{"fleet/queued"}, Intent: "squash",
			Partition: 1, Position: 1, Mine: true, Attention: types.AttentionQueue},
		{ID: "4", Head: "h4", Base: "main", Author: "eli", Intent: "squash",
			Decision: "wait", Code: "WAIT_NOT_APPROVED", Reason: "no approval", Mine: true, Attention: types.AttentionReview},
		{ID: "1", Head: "h1", Branch: "kick", Base: "main", Jobs: []string{"fleet/kick"}, Mark: "kicked_back", Mine: true, Attention: types.AttentionAuthor},
		{ID: "6", Head: "h6", Base: "main", Author: "eli", Mine: true, Attention: types.AttentionNone},
	}, list.Changes, "provider order, another base's change left out, each joined by branch and placed by the plan")
	assert.Equal(t, []string{"fleet/nopr"}, list.Unproposed, "a live job of the tree on a branch no change carries")
}

// The whole tree is the caller's, whichever job it acts under; a login alone claims by
// author.
func TestInflightMineIsTheJobTreeThenTheLogin(t *testing.T) {
	t.Parallel()
	mine := func(me Identity) []string {
		var ids []string
		for _, c := range Inflight(types.JobList{Jobs: inflightRows()}, inflightInput(me)).Changes {
			if c.Mine {
				ids = append(ids, c.ID)
			}
		}
		return ids
	}
	assert.Equal(t, []string{"2", "1"}, mine(Identity{Lease: "fleet/nopr"}), "a sibling's change is the tree's")
	assert.Equal(t, []string{"3"}, mine(Identity{Lease: "other"}))
	assert.Equal(t, []string{"2", "4", "6"}, mine(Identity{Login: "eli"}))
	assert.Empty(t, mine(Identity{}))
}

func TestInflightAttention(t *testing.T) {
	t.Parallel()
	for want, ch := range map[types.InflightAttention]types.InflightChange{
		types.AttentionAuthor: {Intent: "squash", Decision: "kick", Code: "KICK_RED"},
		types.AttentionReview: {Intent: "squash", Decision: "wait", Code: "WAIT_NOT_APPROVED"},
		types.AttentionQueue:  {Intent: "squash", Decision: "wait", Code: "WAIT_CHECKS"},
		types.AttentionNone:   {Mark: "queued"},
	} {
		assert.Equal(t, want, attentionOf(ch), "%+v", ch)
	}
	assert.Equal(t, types.AttentionAuthor, attentionOf(types.InflightChange{Mark: "needs_regeneration"}))
}

// A plan for another base, or for a head that has moved, places nothing.
func TestInflightIgnoresAPlanThatIsNotForTheseHeads(t *testing.T) {
	t.Parallel()
	in := inflightInput(Identity{})
	in.Changes.Changes[1].Head = "moved"
	list := Inflight(types.JobList{Jobs: inflightRows()}, in)
	require.Len(t, list.Changes, 5)
	assert.Zero(t, list.Changes[1].Position, "the head moved since planning")
	assert.Equal(t, 2, list.Changes[0].Position)

	in = inflightInput(Identity{})
	in.Plan.Base = "release"
	list = Inflight(types.JobList{Jobs: inflightRows()}, in)
	assert.Empty(t, list.Fetched.Tip)
	for _, c := range list.Changes {
		assert.Zero(t, c.Position)
		assert.Empty(t, c.Code)
	}
}

func TestInflightNeverFetchedLeavesTheListAlone(t *testing.T) {
	t.Parallel()
	list := types.JobList{Jobs: inflightRows(), Changes: []types.InflightChange{{ID: "stale"}}, Unproposed: []string{"x"}}
	got := Inflight(list, InflightInput{Me: Identity{Lease: "fleet"}})
	assert.Equal(t, types.JobList{Jobs: inflightRows()}, got)
}
