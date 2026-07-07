package knowledge

import (
	"slices"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func routingKind(r types.KnowledgeRouting, kind string) (types.KnowledgeRoutingKind, bool) {
	for _, k := range r.Kinds {
		if k.Kind == kind {
			return k, true
		}
	}
	return types.KnowledgeRoutingKind{}, false
}

func TestRoutingCountsAndKinds(t *testing.T) {
	g := sampleGraph()
	r := g.Routing()

	assert.Equal(t, types.KnowledgeSchemaVersion, r.SchemaVersion)
	assert.Equal(t, len(g.Nodes()), r.NodeCount)
	assert.Equal(t, len(g.Edges()), r.EdgeCount)

	tgt, ok := routingKind(r, types.KindTarget)
	require.True(t, ok, "target kind row present")
	assert.Equal(t, 3, tgt.Count) // pkg/a:build, pkg/a:gen, pkg/b:build
	assert.Contains(t, tgt.Anchors, "build", "the most-connected target is an anchor")
	assert.LessOrEqual(t, len(tgt.Anchors), maxAnchors)

	// Kinds appear in the fixed routingKindOrder (project before target before spell).
	order := map[string]int{}
	for i, k := range routingKindOrder {
		order[k] = i
	}
	got := make([]int, len(r.Kinds))
	for i, k := range r.Kinds {
		got[i] = order[k.Kind]
	}
	assert.True(t, slices.IsSorted(got), "kinds render in routingKindOrder")
}

func TestRoutingProjects(t *testing.T) {
	r := sampleGraph().Routing()

	byPath := map[string]types.KnowledgeRoutingProject{}
	for _, p := range r.Projects {
		byPath[p.Path] = p
	}
	a, ok := byPath["pkg/a"]
	require.True(t, ok, "pkg/a routing row present")
	assert.Equal(t, 2, a.TargetCount) // build, gen
	assert.Contains(t, a.KeyTargets, "build")

	b, ok := byPath["pkg/b"]
	require.True(t, ok)
	assert.Equal(t, 1, b.TargetCount)

	// Projects are sorted by path (deterministic output).
	paths := make([]string, len(r.Projects))
	for i, p := range r.Projects {
		paths[i] = p.Path
	}
	assert.True(t, slices.IsSorted(paths), "projects sorted by path")
}

func TestProjectOfTargetID(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{"target:pkg/a:build", "pkg/a"},
		{"target:.:build", "."},
		{"target:cmd/magus/starter:ci", "cmd/magus/starter"},
	} {
		got, ok := projectOfTargetID(tc.id)
		require.Truef(t, ok, "%s should parse", tc.id)
		assert.Equal(t, tc.want, got)
	}
	_, ok := projectOfTargetID("spell:go")
	assert.False(t, ok, "non-target id does not parse as a project")
}
