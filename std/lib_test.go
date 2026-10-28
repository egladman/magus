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
	assert.Equal(t, []string{"without", "external", "of", "draw", "nodeFact"}, names,
		"only the authoring API, draw and the stamp's node line are exported; the layout and renderer stay private")
}

func TestDiskModulesRegisterWhereTheHookCanImportThem(t *testing.T) {
	watch, ok := GetSource("diskwatch")
	require.True(t, ok, "diskwatch is in std/lib.go's sourceBuiltin")
	assert.Equal(t, "diskwatch", watch.ImportPath(), "a hook may import nothing under magus/")
	reclaim, ok := GetSource("reclaimdisk")
	require.True(t, ok, "reclaimdisk is in std/lib.go's sourceBuiltin")
	assert.Equal(t, "magus/reclaimdisk", reclaim.ImportPath())
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
