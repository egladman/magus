package std

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

func TestDiskModulesRegisterAtTheirMagusPaths(t *testing.T) {
	watch, ok := GetSource("diskwatch")
	require.True(t, ok, "diskwatch is in std/lib.go's sourceBuiltin")
	assert.Equal(t, "magus/diskwatch", watch.ImportPath())
	reclaim, ok := GetSource("reclaimdisk")
	require.True(t, ok, "reclaimdisk is in std/lib.go's sourceBuiltin")
	assert.Equal(t, "magus/reclaimdisk", reclaim.ImportPath())
}

// TestStdLibRefusesMagus holds the std layer. No std/lib source imports a
// magus module or is registered at a magus/ path. The principles page
// buzz-modules-live-in-their-layer.md names this test.
func TestStdLibRefusesMagus(t *testing.T) {
	magusImport := regexp.MustCompile(`(?m)^\s*import\s+"magus[/"]`)
	entries, err := os.ReadDir("lib")
	require.NoError(t, err)
	libSources := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".buzz") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("lib", e.Name()))
		require.NoError(t, err)
		assert.False(t, magusImport.Match(data), "std/lib/%s imports a magus module", e.Name())
		libSources[e.Name()] = string(data)
	}
	require.NotEmpty(t, libSources, "std/lib holds the std sources")
	for _, m := range sourceBuiltin {
		if !strings.HasPrefix(m.ImportPath(), "magus/") {
			continue
		}
		for name, src := range libSources {
			assert.NotEqual(t, src, m.Source, "%s is registered at %s from std/lib/%s", m.Name, m.ImportPath(), name)
		}
	}
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
