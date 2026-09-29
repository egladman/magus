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

func TestDiagramBuzzStringEscapes(t *testing.T) {
	assert.Equal(t, `"a\"b\\c\{d\}\n\t\007"`, buzzString("a\"b\\c{d}\n\t\x07"))
}
