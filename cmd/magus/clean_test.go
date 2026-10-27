package main

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A caller parsing -o json reads the same shape whatever clean found: lists, never
// null, and never empty stdout.
func TestCleanCmdReportsEmptyListsWhenNothingIsRemoved(t *testing.T) {
	testkit.Isolate(t)
	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	root := w.Root()
	prev := global.output
	t.Cleanup(func() { global.output = prev })
	global.output = "json"

	out := captureStdout(t, func() {
		require.NoError(t, cleanCommand(context.Background(), root, nil))
	})

	var got types.CleanReport
	require.NoError(t, json.Unmarshal([]byte(out), &got), "stdout: %q", out)
	assert.Equal(t, types.CleanReport{Removed: []string{}, Tracked: []string{}}, got)
}
