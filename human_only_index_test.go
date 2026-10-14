package magus

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/gen/mocks"
)

// TestHumanOnlyIndexStopsAtTheFirstHumanOnlyTarget pins the walk the record shares with
// the guard: a composer reaches a human_only target through any depth and across projects,
// but never through another human_only target, whose approval covers its own chain.
func TestHumanOnlyIndexStopsAtTheFirstHumanOnlyTarget(t *testing.T) {
	root := &types.Project{
		Path: ".",
		TargetPolicies: map[string]types.Target{
			"bench": {HumanOnly: true, HumanOnlyReason: "launches paid sessions", SkipCache: true},
			"gated": {HumanOnly: true, HumanOnlyReason: "asks a person"},
			"inner": {HumanOnly: true, HumanOnlyReason: "runs under gated"},
			"lint":  {SkipCache: true},
		},
		TargetChains: map[string][]types.ChainStep{
			"suite": {{Target: "bench"}, {Target: "lint"}},
			"outer": {{Target: "suite"}},
			"cross": {{Project: "evals", Target: "grade"}},
			"gated": {{Target: "inner"}},
			"via":   {{Target: "gated"}},
		},
	}
	evals := &types.Project{Path: "evals", TargetPolicies: map[string]types.Target{"grade": {HumanOnly: true, HumanOnlyReason: "spends tokens"}}}
	projects := []*types.Project{root, evals}
	ws := mocks.NewMockWorkspaceReader(t)
	ws.EXPECT().All().Return(projects)
	ws.EXPECT().Get(mock.Anything).RunAndReturn(func(path string) *types.Project {
		for _, p := range projects {
			if p.Path == path {
				return p
			}
		}
		return nil
	}).Maybe()

	ref := func(project, target string) types.TargetRef { return types.TargetRef{Project: project, Target: target} }
	assert.Equal(t, types.HumanOnlyIndex{
		Projects: []string{".", "evals"},
		Targets: []types.HumanOnlyTarget{
			{TargetRef: ref(".", "bench"), Reason: "launches paid sessions", SkipCache: true, ReachedBy: []types.TargetRef{ref(".", "outer"), ref(".", "suite")}},
			{TargetRef: ref(".", "gated"), Reason: "asks a person", ReachedBy: []types.TargetRef{ref(".", "via")}},
			{TargetRef: ref(".", "inner"), Reason: "runs under gated"},
			{TargetRef: ref("evals", "grade"), Reason: "spends tokens", ReachedBy: []types.TargetRef{ref(".", "cross")}},
		},
	}, humanOnlyIndex(ws))
}

// TestHumanOnlyIndexOfAWorkspaceDeclaringNone pins the cheap case: the projects are listed
// so a reader can tell a declaration of none from no record, and no chain is walked.
func TestHumanOnlyIndexOfAWorkspaceDeclaringNone(t *testing.T) {
	ws := mocks.NewMockWorkspaceReader(t)
	ws.EXPECT().All().Return([]*types.Project{{Path: ".", TargetChains: map[string][]types.ChainStep{"ci": {{Target: "test"}}}}})
	assert.Equal(t, types.HumanOnlyIndex{Projects: []string{"."}}, humanOnlyIndex(ws))
}
