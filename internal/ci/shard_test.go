package ci

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// TestShardSkillsDeriveFromWhatTheShardDoes pins the derivation, not a table. A second
// copy of the routing table would drift the first time a project changed spells; deriving
// it from the shard's own declarations cannot. Each verdict carries its reason because a
// routing decision an agent cannot audit is one it should not act on.
func TestShardSkillsDeriveFromWhatTheShardDoes(t *testing.T) {
	t.Parallel()

	skills, why := shardSkills(ShardDetail{})
	assert.Equal(t, []string{"magus-run-full"}, skills, "every shard is a target invocation, named as the twin a delegate can rely on")
	assert.Len(t, why, 2, "the verdict plus the variant note")

	skills, why = shardSkills(ShardDetail{Writes: []string{"gen/*.json"}})
	assert.Equal(t, []string{"magus-run-full", "magus-vcs-hygiene-full"}, skills,
		"a shard that writes declared outputs leaves generated files behind, which is magus-vcs-hygiene's whole subject")
	assert.Len(t, why, 3)
}

func TestAppendUniquePreservesOrderAndDropsBlanks(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"go", "buzz"}, AppendUnique(nil, "go", "", "buzz", "go"))
}

// TestFilterShardsIntersectsRatherThanReplaces pins the distinction the filter exists for.
// `magus run ci docs` runs docs whether or not the diff touched it; this answers "of the
// work this change implies, give me the docs part", which is what an orchestrator
// splitting an affected set across agents is actually asking.
func TestFilterShardsIntersectsRatherThanReplaces(t *testing.T) {
	t.Parallel()
	shards := []types.Shard{
		{ID: "0", ProjectPaths: []string{".", "docs"}},
		{ID: "1", ProjectPaths: []string{"console"}},
		{ID: "2", ProjectPaths: []string{"proto"}},
	}

	got := filterShardPaths(shards, map[string]bool{"docs": true, "proto": true})

	// A shard left with nothing drops out, and the unselected project leaves the shard it
	// shared. IDs are preserved, not renumbered, so a filtered plan reads against the one
	// it came from: shard 1 is absent rather than shard 2 being renamed to 1.
	assert.Equal(t, []types.Shard{
		{ID: "0", Digest: types.UnitsDigest(nil), ProjectPaths: []string{"docs"}},
		{ID: "2", Digest: types.UnitsDigest(nil), ProjectPaths: []string{"proto"}},
	}, got)
}

type FilterShardUnitsSuite struct{ suite.Suite }

func TestFilterShardUnitsSuite(t *testing.T) { suite.Run(t, new(FilterShardUnitsSuite)) }

func (s *FilterShardUnitsSuite) TestDropsTheUnitsOfUnselectedProjects() {
	got := filterShardPaths([]types.Shard{{
		ID:           "0",
		ProjectPaths: []string{".", "docs"},
		Units:        []types.Unit{{Project: ".", Target: "test"}, {Project: "docs", Target: "lint"}},
	}}, map[string]bool{"docs": true})
	kept := []types.Unit{{Project: "docs", Target: "lint"}}
	s.Equal([]types.Shard{{ID: "0", Digest: types.UnitsDigest(kept), ProjectPaths: []string{"docs"}, Units: kept}}, got)
}

func (s *FilterShardUnitsSuite) TestKeepsTheMatrixFreeOfUnits() {
	out := PlanOutput{Count: 1, Matrix: []PlanShard{{Shard: "0", Projects: ".", JobName: ".", Digest: "d", Duties: "cache-export", Units: []types.Unit{{Project: ".", Target: "test"}}}}}
	published, err := PlanOutputs(out)
	s.Require().NoError(err)
	s.JSONEq(`{"include":[{"shard":"0","projects":".","job_name":".","digest":"d","duties":"cache-export"}]}`, published[0].Value)
	s.Len(out.Matrix[0].Units, 1, "the plan document keeps its units")
}

// TestFilterShardPathsEmptyWhenNothingMatches: a real project outside the affected set is
// the honest empty answer, not an error: that is precisely the question being asked.
func TestFilterShardPathsEmptyWhenNothingMatches(t *testing.T) {
	t.Parallel()
	got := filterShardPaths([]types.Shard{{ID: "0", ProjectPaths: []string{"docs"}}}, map[string]bool{"libs/foo": true})
	assert.Empty(t, got)
}

// ShardNameSuite pins the split between the two fields: Projects is checked
// against the units, Name is only read by a person scanning a job list.
type ShardNameSuite struct{ suite.Suite }

func TestShardNameSuite(t *testing.T) { suite.Run(t, new(ShardNameSuite)) }

func (s *ShardNameSuite) TestNoProjectsSaysSo() {
	s.Equal("no projects", shardName(nil))
}

func (s *ShardNameSuite) TestUpToThreeProjectsAreListed() {
	s.Equal(". docs", shardName([]string{".", "docs"}))
	s.Equal(". docs proto", shardName([]string{".", "docs", "proto"}))
}

func (s *ShardNameSuite) TestMoreThanThreeElideTheRest() {
	s.Equal(". docs proto +2", shardName([]string{".", "docs", "proto", "console", "libs/textsearch"}))
}

type fakeProjects []string

func (f fakeProjects) ListProjects(context.Context) (types.ProjectsOutput, error) {
	var out types.ProjectsOutput
	for _, p := range f {
		out.Projects = append(out.Projects, types.ProjectEntry{Path: p})
	}
	return out, nil
}

// A typo is an error rather than an empty plan; a real project outside the plan is not.
func TestFilterShardsRefusesAnUnknownProject(t *testing.T) {
	t.Parallel()
	shards := []types.Shard{{ID: "0", ProjectPaths: []string{"docs"}}}

	_, err := FilterShards(context.Background(), fakeProjects{"docs", "console"}, shards, []string{"dcos"})
	assert.ErrorContains(t, err, `no project "dcos" in this workspace`)

	got, err := FilterShards(context.Background(), fakeProjects{"docs", "console"}, shards, []string{"console/"})
	require.NoError(t, err)
	assert.Empty(t, got)
}
