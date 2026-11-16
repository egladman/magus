package job

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	qtypes "github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

type inflightDetailSuite struct {
	suite.Suite
}

func TestInflightDetail(t *testing.T) { suite.Run(t, new(inflightDetailSuite)) }

func (s *inflightDetailSuite) TestOnePartitionNamesOnlyThePosition() {
	s.Equal("pos 2", InflightDetail(InflightChange{Partition: 1, Position: 2}, 1))
}

func (s *inflightDetailSuite) TestSeveralPartitionsNameThePartition() {
	s.Equal("partition 1 pos 2", InflightDetail(InflightChange{Partition: 1, Position: 2}, 3))
}

func (s *inflightDetailSuite) TestACodeWithAReasonOutranksTheMark() {
	c := InflightChange{Position: 1, Code: "MGS1", Reason: "why", Mark: "needs_work"}

	s.Equal("pos 1  MGS1: why", InflightDetail(c, 1))
}

func (s *inflightDetailSuite) TestAMarkReadsAsWords() {
	s.Equal("needs work", InflightDetail(InflightChange{Mark: "needs_work"}, 0))
}

func (s *inflightDetailSuite) TestNothingToSayIsEmpty() {
	s.Empty(InflightDetail(InflightChange{}, 0))
}

func inflightRows() []job.Job {
	return []job.Job{
		{ID: "fleet", State: job.StateRunning},
		{ID: "fleet/kick", Parent: "fleet", State: job.StatePass, CheckoutRoot: "/w/kick"},
		{ID: "fleet/queued", Parent: "fleet", State: job.StateRunning, CheckoutRoot: "/w/queued"},
		{ID: "fleet/nopr", Parent: "fleet", State: job.StateRunning, CheckoutRoot: "/w/nopr"},
		{ID: "other", State: job.StateRunning},
		{ID: "other/work", Parent: "other", State: job.StateRunning, CheckoutRoot: "/w/theirs"},
	}
}

func inflightInput(me Identity) InflightInput {
	queued := qtypes.Change{ID: "2", Head: "h2", Branch: "queued", Base: "main", Title: "queued", Author: "eli", Method: qtypes.MethodSquash}
	theirs := qtypes.Change{ID: "3", Head: "h3", Branch: "theirs", Base: "main", Author: "ann", Method: qtypes.MethodRebase}
	unapproved := qtypes.Change{ID: "4", Head: "h4", Base: "main", Author: "eli", Method: qtypes.MethodSquash}
	return InflightInput{
		Fetch: &InflightFetch{Provider: "github", Host: "github.com", Base: "main", At: 100, ElapsedMS: 7},
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
	list := Inflight(job.List{Jobs: inflightRows()}, inflightInput(Identity{Lease: "fleet/queued", Login: "eli"}))

	assert.Equal(t, InflightList{
		List:    job.List{Jobs: inflightRows()},
		Fetched: &InflightFetch{Provider: "github", Host: "github.com", Base: "main", Tip: "tip", At: 100, ElapsedMS: 7},
		// Provider order, another base's change left out, each joined by branch and placed by the plan.
		Changes: []InflightChange{
			{ID: "3", Head: "h3", Branch: "theirs", Base: "main", Author: "ann", Jobs: []string{"other/work"}, Intent: "rebase",
				Partition: 1, Position: 2, Attention: AttentionQueue},
			{ID: "2", Title: "queued", Head: "h2", Branch: "queued", Base: "main", Author: "eli", Jobs: []string{"fleet/queued"}, Intent: "squash",
				Partition: 1, Position: 1, Mine: true, Attention: AttentionQueue},
			{ID: "4", Head: "h4", Base: "main", Author: "eli", Intent: "squash",
				Decision: "wait", Code: "WAIT_NOT_APPROVED", Reason: "no approval", Mine: true, Attention: AttentionReview},
			{ID: "1", Head: "h1", Branch: "kick", Base: "main", Jobs: []string{"fleet/kick"}, Mark: "kicked_back", Mine: true, Attention: AttentionAuthor},
			{ID: "6", Head: "h6", Base: "main", Author: "eli", Mine: true, Attention: AttentionNone},
		},
		// A live job of the tree on a branch no change carries.
		Unproposed: []string{"fleet/nopr"},
	}, list)
}

// The whole tree is the caller's, whichever job it acts under; a login alone claims by
// author.
func TestInflightMineIsTheJobTreeThenTheLogin(t *testing.T) {
	t.Parallel()
	mine := func(me Identity) []string {
		var ids []string
		for _, c := range Inflight(job.List{Jobs: inflightRows()}, inflightInput(me)).Changes {
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
	for want, ch := range map[InflightAttention]InflightChange{
		AttentionAuthor: {Intent: "squash", Decision: "kick", Code: "KICK_RED"},
		AttentionReview: {Intent: "squash", Decision: "wait", Code: "WAIT_NOT_APPROVED"},
		AttentionQueue:  {Intent: "squash", Decision: "wait", Code: "WAIT_CHECKS"},
		AttentionNone:   {Mark: "queued"},
	} {
		assert.Equal(t, want, changeAttention(ch), "%+v", ch)
	}
	assert.Equal(t, AttentionAuthor, changeAttention(InflightChange{Mark: "needs_regeneration"}))
}

// A plan for another base, or for a head that has moved, places nothing.
func TestInflightIgnoresAPlanThatIsNotForTheseHeads(t *testing.T) {
	t.Parallel()
	in := inflightInput(Identity{})
	in.Changes.Changes[1].Head = "moved"
	list := Inflight(job.List{Jobs: inflightRows()}, in)
	require.Len(t, list.Changes, 5)
	assert.Zero(t, list.Changes[1].Position, "the head moved since planning")
	assert.Equal(t, 2, list.Changes[0].Position)

	in = inflightInput(Identity{})
	in.Plan.Base = "release"
	list = Inflight(job.List{Jobs: inflightRows()}, in)
	assert.Empty(t, list.Fetched.Tip)
	for _, c := range list.Changes {
		assert.Zero(t, c.Position)
		assert.Empty(t, c.Code)
	}
}

func TestInflightNeverFetchedJoinsNothing(t *testing.T) {
	t.Parallel()
	got := Inflight(job.List{Jobs: inflightRows()}, InflightInput{Me: Identity{Lease: "fleet"}})
	assert.Equal(t, InflightList{List: job.List{Jobs: inflightRows()}}, got)
}
