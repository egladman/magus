package std

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFigureRegistersAtItsImportPath(t *testing.T) {
	got, ok := GetSource("figure")
	require.True(t, ok, "figure is in std/lib.go's sourceBuiltin")
	assert.Equal(t, "magus/figure", got.ImportPath())

	methods, err := describeSource(got)
	require.NoError(t, err)
	names := make([]string, 0, len(methods))
	for _, m := range methods {
		names = append(names, m.Name)
	}
	assert.Equal(t, []string{"without", "external", "of", "draw"}, names,
		"only the authoring API and draw are exported; the layout and renderer stay private")
}

func TestModelTiersRegistersTheDefaultDeclaration(t *testing.T) {
	got, ok := GetSource("modeltiers")
	require.True(t, ok, "modeltiers is in std/lib.go's sourceBuiltin")
	assert.Equal(t, "magus/modeltiers", got.ImportPath())

	methods, err := describeSource(got)
	require.NoError(t, err)
	names := make([]string, 0, len(methods))
	for _, m := range methods {
		names = append(names, m.Name)
	}
	assert.Equal(t, []string{"bounds", "skills"}, names)
}
