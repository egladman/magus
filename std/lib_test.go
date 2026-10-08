package std

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFigureRegistersAtItsImportPath(t *testing.T) {
	got, ok := GetSource("figure")
	require.True(t, ok, "figure registers from std/lib.go's init")
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
