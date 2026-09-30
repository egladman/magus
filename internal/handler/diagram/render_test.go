package diagram

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagramCutWalksBothDirections(t *testing.T) {
	g := projectGraph(chainWorkspace().targets)

	cut, err := g.cut(Lens{Focus: "libs/lib", Depth: 1})
	require.NoError(t, err)
	assert.Equal(t, []string{"app", "libs/lib", "libs/core"}, anchors(cut.nodes))
	assert.Equal(t, []edge{{src: "app", dst: "libs-lib"}, {src: "libs-lib", dst: "libs-core"}}, cut.edges)

	cut, err = g.cut(Lens{Focus: "libs/lib"})
	require.NoError(t, err)
	assert.Equal(t, []string{"libs/lib"}, anchors(cut.nodes))
	assert.Empty(t, cut.edges)

	cut, err = g.cut(Lens{Scope: []string{"libs/"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"libs/lib", "libs/core", "libs/base"}, anchors(cut.nodes))
	assert.Len(t, cut.edges, 2, "the app -> lib edge leaves with app")
}

func TestDiagramIDsStayUnique(t *testing.T) {
	taken := ids{}
	assert.Equal(t, "libs-lib", taken.of("libs/lib"))
	assert.Equal(t, "libs-lib-2", taken.of("libs-lib"))
	assert.Equal(t, "root", taken.of("."))
}

func TestDiagramActorNamesStayApart(t *testing.T) {
	names := actorNames([]Node{{ID: "a", Anchor: "x", Label: "core"}, {ID: "b", Anchor: "y", Label: "core"}, {ID: "c", Anchor: "z", Label: "app"}})
	assert.Equal(t, map[string]string{"a": "core (x)", "b": "core (y)", "c": "app"}, names)
	assert.Equal(t, "https://h/blob/r/libs/lib", linkTo("https://h/blob/r/{path}", "libs/lib"))
	assert.Empty(t, linkTo("", "libs/lib"))
}
