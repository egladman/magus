package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

// humanOnlyWorkspace answers the two reads IndexHumanOnly makes: the projects and a lookup
// by path. The generated WorkspaceReader mock would close an import cycle here.
type humanOnlyWorkspace struct {
	WorkspaceReader
	projects []*Project
}

func (w humanOnlyWorkspace) All() []*Project { return w.projects }

func (w humanOnlyWorkspace) Get(path string) *Project {
	for _, p := range w.projects {
		if p.Path == path {
			return p
		}
	}
	return nil
}

// TestHumanOnlyIndexRoundTrips pins the shape the knowledge manifest stores: a target's
// ref flattened beside its reason, and an index that declares nothing encoded as an empty
// object, so a manifest records "none declared" in two bytes.
func TestHumanOnlyIndexRoundTrips(t *testing.T) {
	ix := HumanOnlyIndex{
		Projects: []string{".", "evals"},
		Targets: []HumanOnlyTarget{{
			TargetRef: TargetRef{Project: ".", Target: "agent-bench-run"},
			Reason:    "launches paid sessions",
			SkipCache: true,
			ReachedBy: []TargetRef{{Project: ".", Target: "bench-all"}},
		}},
	}
	b, err := json.Marshal(ix)
	require.NoError(t, err)
	assert.JSONEq(t, `{"projects": [".", "evals"], "targets": [{"project": ".", "target": "agent-bench-run",
		"reason": "launches paid sessions", "skip_cache": true, "reached_by": [{"project": ".", "target": "bench-all"}]}]}`, string(b))

	var back HumanOnlyIndex
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, ix, back)

	none, err := json.Marshal(HumanOnlyIndex{})
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(none))
}

// TestIndexHumanOnlyStopsAtTheFirstHumanOnlyTarget pins the walk the record and the guard
// share: a composer reaches a human_only target through any depth and across projects, but
// never through another human_only target, whose approval covers its own chain.
func TestIndexHumanOnlyStopsAtTheFirstHumanOnlyTarget(t *testing.T) {
	root := &Project{
		Path: ".",
		TargetPolicies: map[string]Target{
			"bench": {HumanOnly: true, HumanOnlyReason: "launches paid sessions", SkipCache: true},
			"gated": {HumanOnly: true, HumanOnlyReason: "asks a person"},
			"inner": {HumanOnly: true, HumanOnlyReason: "runs under gated"},
			"lint":  {SkipCache: true},
		},
		TargetChains: map[string][]ChainStep{
			"suite": {{Target: "bench"}, {Target: "lint"}},
			"outer": {{Target: "suite"}},
			"cross": {{Project: "evals", Target: "grade"}},
			"gated": {{Target: "inner"}},
			"via":   {{Target: "gated"}},
		},
	}
	evals := &Project{Path: "evals", TargetPolicies: map[string]Target{"grade": {HumanOnly: true, HumanOnlyReason: "spends tokens"}}}
	ws := humanOnlyWorkspace{projects: []*Project{root, evals}}

	ref := func(project, target string) TargetRef { return TargetRef{Project: project, Target: target} }
	assert.Equal(t, HumanOnlyIndex{
		Projects: []string{".", "evals"},
		Targets: []HumanOnlyTarget{
			{TargetRef: ref(".", "bench"), Reason: "launches paid sessions", SkipCache: true, ReachedBy: []TargetRef{ref(".", "outer"), ref(".", "suite")}},
			{TargetRef: ref(".", "gated"), Reason: "asks a person", ReachedBy: []TargetRef{ref(".", "via")}},
			{TargetRef: ref(".", "inner"), Reason: "runs under gated"},
			{TargetRef: ref("evals", "grade"), Reason: "spends tokens", ReachedBy: []TargetRef{ref(".", "cross")}},
		},
	}, IndexHumanOnly(ws))
}

// TestIndexHumanOnlyOfAWorkspaceDeclaringNone pins the cheap case: the projects are listed
// so a reader can tell a declaration of none from no record, and no chain is walked.
func TestIndexHumanOnlyOfAWorkspaceDeclaringNone(t *testing.T) {
	ws := humanOnlyWorkspace{projects: []*Project{{Path: ".", TargetChains: map[string][]ChainStep{"ci": {{Target: "test"}}}}}}
	assert.Equal(t, HumanOnlyIndex{Projects: []string{"."}}, IndexHumanOnly(ws))
}
