package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A caller parsing -o json reads the same shape whatever clean found: lists, never
// null, and never empty stdout.
func TestCleanCmdReportsEmptyListsWhenNothingIsRemoved(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))
	prev := global.output
	t.Cleanup(func() { global.output = prev })
	global.output = "json"

	out := captureStdout(t, func() {
		require.NoError(t, cleanCmd(context.Background(), root, nil))
	})

	var got types.CleanReport
	require.NoError(t, json.Unmarshal([]byte(out), &got), "stdout: %q", out)
	assert.Equal(t, types.CleanReport{Removed: []string{}, Tracked: []string{}}, got)
}
