package std

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagramRegistersAtItsImportPath(t *testing.T) {
	got, ok := GetSource("diagram")
	require.True(t, ok, "diagram is in std/lib.go's sourceBuiltin")
	assert.Equal(t, "magus/diagram", got.ImportPath())

	methods, err := describeSource(got)
	require.NoError(t, err)
	names := make([]string, 0, len(methods))
	for _, m := range methods {
		names = append(names, m.Name)
	}
	assert.Equal(t, []string{"without", "person", "external", "step", "stage", "of", "draw", "drawLayout", "nodeFact", "drillLinks", "claims", "atlas"}, names,
		"only the authoring API, draw, drawLayout and the stamp's node line, drillLinks, claims and atlas are exported: Diagram.layout() hands a caller the exported Layout, and drawLayout paints it in each theme without a second layout; the layout functions and the renderer stay private")
}

func TestDiskModulesRegisterWhereTheHookCanImportThem(t *testing.T) {
	watch, ok := GetSource("diskwatch")
	require.True(t, ok, "diskwatch is in std/lib.go's sourceBuiltin")
	assert.Equal(t, "diskwatch", watch.ImportPath(), "a hook may import nothing under magus/")
	reclaim, ok := GetSource("reclaimdisk")
	require.True(t, ok, "reclaimdisk is in std/lib.go's sourceBuiltin")
	assert.Equal(t, "magus/reclaimdisk", reclaim.ImportPath())
}

func TestResultRegistersWhereAHookCanImportIt(t *testing.T) {
	got, ok := GetSource("result")
	require.True(t, ok, "result is in std/lib.go's sourceBuiltin")
	assert.Equal(t, "result", got.ImportPath(), "a hook may import nothing under magus/")

	methods, err := describeSource(got)
	require.NoError(t, err)
	names := make([]string, 0, len(methods))
	for _, m := range methods {
		names = append(names, m.Name)
	}
	assert.Equal(t, []string{"ok", "fail", "caught"}, names)
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
