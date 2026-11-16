package ci

import (
	"fmt"
	"testing"

	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestDecodeSavedPlan(t *testing.T) {
	t.Parallel()

	one := []types.Unit{{Project: ".", Target: "test"}, {Project: "docs", Target: "lint"}}
	two := []types.Unit{{Project: "console", Target: "lint"}}
	got, err := decodeSavedPlan(fmt.Appendf(nil, `{"target":"ci","count":2,"max_parallel":2,"future_key":true,
		"matrix":[{"shard":"1","projects":". docs","digest":%q,"units":[{"project":".","target":"test"},{"project":"docs","target":"lint"}]},
			{"shard":"2","projects":"console","digest":%q,"units":[{"project":"console","target":"lint"}]}],
		"outputs":[{"name":"count","value":"2"}]}`, types.UnitsDigest(one), types.UnitsDigest(two)))
	require.NoError(t, err)
	assert.Equal(t, PlanOutput{
		Target:      "ci",
		Count:       2,
		MaxParallel: 2,
		Matrix: []PlanShard{
			{Shard: "1", Projects: ". docs", Digest: types.UnitsDigest(one), Units: one},
			{Shard: "2", Projects: "console", Digest: types.UnitsDigest(two), Units: two},
		},
		Outputs: []PlanPublish{{Name: "count", Value: "2"}},
	}, got)

	refused := []struct {
		name, input, want string
	}{
		{"not json", `nope`, "the plan on stdin is not a shard plan"},
		{"empty stdin", ``, "the plan on stdin is not a shard plan"},
		{"no target", `{"count":0,"matrix":[]}`, "names no target"},
		{"count disagrees", `{"target":"ci","count":2,"matrix":[{"shard":"1","projects":"."}]}`, "states 2 shard(s) but lists 1"},
		{"shard without id", `{"target":"ci","count":1,"matrix":[{"projects":"."}]}`, "a shard with no id"},
		{"duplicate id", fmt.Sprintf(`{"target":"ci","count":2,"matrix":[{"shard":"1","projects":".","digest":"%s","units":[{"project":".","target":"test"}]},{"shard":"1","projects":"docs"}]}`, types.UnitsDigest([]types.Unit{{Project: ".", Target: "test"}})), "lists shard 1 twice"},
		{"no projects", `{"target":"ci","count":1,"matrix":[{"shard":"1","projects":"  "}]}`, "shard 1 has no projects"},
		{"no units", `{"target":"ci","count":1,"matrix":[{"shard":"1","projects":"."}]}`, "shard 1 lists no units"},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeSavedPlan([]byte(tt.input))
			require.ErrorIs(t, err, types.SavedPlanRefused)
			assert.ErrorContains(t, err, tt.want)
			assert.Equal(t, exit.Usage, exit.Code(err))
		})
	}
}

type SelectPlanShardsSuite struct {
	suite.Suite
	plan PlanOutput
}

func TestSelectPlanShardsSuite(t *testing.T) { suite.Run(t, new(SelectPlanShardsSuite)) }

func (s *SelectPlanShardsSuite) SetupTest() {
	s.plan = PlanOutput{Target: "ci", Count: 2, Matrix: []PlanShard{
		{Shard: "1", Projects: ". docs"},
		{Shard: "2", Projects: "console"},
	}}
}

func (s *SelectPlanShardsSuite) TestAShardRunsOnlyItsOwnUnits() {
	got, err := SelectPlanShards(s.plan, "ci", "2", 0)
	s.Require().NoError(err)
	s.Equal([]PlanShard{{Shard: "2", Projects: "console"}}, got)
}

func (s *SelectPlanShardsSuite) TestAMultiShardPlanWithNoSelectionRefuses() {
	got, err := SelectPlanShards(s.plan, "ci", "", 0)
	s.Require().ErrorIs(err, types.SavedPlanRefused)
	s.ErrorContains(err, "the plan has 2 shards and no --shard selects one")
	s.ErrorContains(err, "--shard <id> (1, 2)")
	s.ErrorContains(err, "--shard all")
	s.Nil(got)
}

func (s *SelectPlanShardsSuite) TestAllRunsEveryShard() {
	got, err := SelectPlanShards(s.plan, "ci", "all", 0)
	s.Require().NoError(err)
	s.Equal(s.plan.Matrix, got)
}

func (s *SelectPlanShardsSuite) TestAOneShardPlanRunsWithNoSelection() {
	s.plan.Count, s.plan.Matrix = 1, s.plan.Matrix[:1]
	got, err := SelectPlanShards(s.plan, "ci", "", 0)
	s.Require().NoError(err)
	s.Equal(s.plan.Matrix, got)
}

func (s *SelectPlanShardsSuite) TestAnUnknownShardRefuses() {
	_, err := SelectPlanShards(s.plan, "ci", "3", 0)
	s.Require().ErrorIs(err, types.SavedPlanRefused)
	s.ErrorContains(err, "--shard 3: the plan has no such shard (it has 1, 2)")
}

func (s *SelectPlanShardsSuite) TestAnotherTargetRefuses() {
	_, err := SelectPlanShards(s.plan, "test", "1", 0)
	s.Require().ErrorIs(err, types.SavedPlanRefused)
	s.ErrorContains(err, "the plan is for ci, not test")
}

func (s *SelectPlanShardsSuite) TestADisagreeingShardCountRefuses() {
	_, err := SelectPlanShards(s.plan, "ci", "1", 3)
	s.Require().ErrorIs(err, types.SavedPlanRefused)
	s.ErrorContains(err, "--n-shards 3, but the plan has 2 shard(s)")
}

func (s *SelectPlanShardsSuite) TestAPlanWithNoShardsRunsWholeAndRefusesASelection() {
	empty := PlanOutput{Target: "ci"}
	got, err := SelectPlanShards(empty, "ci", "", 0)
	s.Require().NoError(err)
	s.Empty(got)
	_, err = SelectPlanShards(empty, "ci", "1", 0)
	s.Require().ErrorIs(err, types.SavedPlanRefused)
	s.ErrorContains(err, "the plan has no shards, so there is nothing to select")
}

func TestPlanProjectsKeepPlanOrder(t *testing.T) {
	t.Parallel()

	got := PlanProjects([]PlanShard{{Projects: ". docs"}, {Projects: "console  libs/a"}})
	assert.Equal(t, []string{".", "docs", "console", "libs/a"}, got)
}

type SavedPlanAgreementSuite struct{ suite.Suite }

func TestSavedPlanAgreementSuite(t *testing.T) { suite.Run(t, new(SavedPlanAgreementSuite)) }

func (s *SavedPlanAgreementSuite) decode(projects, label string) error {
	_, err := decodeSavedPlan(fmt.Appendf(nil,
		`{"target":"ci","count":1,"matrix":[{"shard":"0","projects":%q,"digest":%q,"units":[{"project":"docs","target":"lint"}]}]}`,
		projects, label))
	return err
}

func (s *SavedPlanAgreementSuite) TestRefusesProjectsTheUnitsDoNotRunIn() {
	err := s.decode(". docs", types.UnitsDigest([]types.Unit{{Project: "docs", Target: "lint"}}))
	s.Require().ErrorIs(err, types.SavedPlanRefused)
	s.ErrorContains(err, `shard 0 lists projects ". docs", but its units run in "docs"`)
}

func (s *SavedPlanAgreementSuite) TestRefusesADigestThatIsNotTheUnitsDigest() {
	err := s.decode("docs", "docs")
	s.Require().ErrorIs(err, types.SavedPlanRefused)
	s.ErrorContains(err, `shard 0 has digest "docs", but its units digest to`)
}

func (s *SavedPlanAgreementSuite) TestAcceptsAShardWhoseProjectsAndDigestAgree() {
	s.NoError(s.decode("docs", types.UnitsDigest([]types.Unit{{Project: "docs", Target: "lint"}})))
}

type PlanTargetsSuite struct{ suite.Suite }

func TestPlanTargetsSuite(t *testing.T) { suite.Run(t, new(PlanTargetsSuite)) }

func (s *PlanTargetsSuite) TestRunsEachShardUnitAsItsOwnTarget() {
	got := PlanTargets([]PlanShard{{Units: []types.Unit{{Project: ".", Target: "test"}, {Project: "libs/a", Target: "format"}}}})
	s.Equal([]types.Target{{Path: ".", Name: "test"}, {Path: "libs/a", Name: "format"}}, got)
}

func (s *PlanTargetsSuite) TestAUnitTwoShardsHoldRunsOnce() {
	u := types.Unit{Project: ".", Target: "lint"}
	got := PlanTargets([]PlanShard{{Units: []types.Unit{u}}, {Units: []types.Unit{u}}})
	s.Equal([]types.Target{{Path: ".", Name: "lint"}}, got)
}

type SameCharmsSuite struct{ suite.Suite }

func TestSameCharmsSuite(t *testing.T) { suite.Run(t, new(SameCharmsSuite)) }

func (s *SameCharmsSuite) TestOrderDoesNotMatter() {
	s.True(SameCharms([]string{"gha", "ci"}, []string{"ci", "gha"}))
}

func (s *SameCharmsSuite) TestADifferentCharmDiffers() {
	s.False(SameCharms([]string{"gha"}, []string{"ci"}))
}

func (s *SameCharmsSuite) TestNoCharmsEqualsNoCharms() {
	s.True(SameCharms(nil, []string{}))
}
