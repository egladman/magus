package main

import (
	"context"
	"errors"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionShowNamesTheCostSectionsItCouldNotMeasure(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := t.TempDir()
	_, err := loadStream(t, root, `{"host":"h1","session":"s1","ts":1,"kind":"shell.command","ref":"r1","text":"ls"}`)
	require.NoError(t, err)

	out := captureStdout(t, func() { require.NoError(t, sessionShow(root, []string{"s1"})) })

	assert.Contains(t, out, "Cost, with the root as the main thread:")
	assert.Contains(t, out, "root context     not measured: this checkout's trail holds nothing for the session")
	assert.NotContains(t, out, "$0.00")
}

func TestHarnessModelRowsOpenTheWorkspaceOnceAndOnlyWhenAsked(t *testing.T) {
	opened := 0
	rows := harnessModelRows(t.Context(), t.TempDir(), func(context.Context) (types.WorkspaceRepository, error) {
		opened++
		return nil, errors.New("magusfile.buzz does not load")
	})

	assert.Zero(t, opened, "building the reader reads nothing")
	assert.Empty(t, rows(), "a workspace that will not load has no rows")
	assert.Empty(t, rows())
	assert.Equal(t, 1, opened)
}
