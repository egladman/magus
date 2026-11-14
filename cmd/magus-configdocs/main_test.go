package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config/gen/settings"
	"github.com/egladman/magus/libs/clikit/command"
)

// TestRenderNamesEveryField: the reference is the settings table, so each
// key and its environment variable reach the page. Whether the committed page is this
// render is docs generate's drift gate.
func TestRenderNamesEveryField(t *testing.T) {
	page, err := render()
	require.NoError(t, err)
	for _, f := range settings.Settings {
		assert.Contains(t, page, fmt.Sprintf("`%s`", f.YamlPath))
		if f.EnvVar != "" {
			assert.Contains(t, page, fmt.Sprintf("`%s`", f.EnvVar))
		}
	}
}

func TestRenderListsWhatMagusSetsApart(t *testing.T) {
	page, err := render()
	require.NoError(t, err)
	_, setByMagus, ok := strings.Cut(page, "## Set by magus")
	require.True(t, ok)
	assert.Contains(t, setByMagus, "`MAGUS_LEVEL`")
	assert.NotContains(t, setByMagus, "`MAGUS_OFFLINE`")
}

func TestDefaultCellSetsAURLInCode(t *testing.T) {
	assert.Equal(t, "`https://s3.example.com`", defaultCell("https://s3.example.com"))
}

func TestDefaultCellLeavesProseAsItIs(t *testing.T) {
	assert.Equal(t, "the hosted log viewer", defaultCell("the hosted log viewer"))
}

func TestKindLabelNamesTheKindItCannotLabel(t *testing.T) {
	_, err := kindLabel(command.Kind("mystery"))
	require.Error(t, err)
	assert.ErrorContains(t, err, `setting kind "mystery" has no label`)
}
