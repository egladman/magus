package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The graph of a checkout the process did not open answers from that checkout's own
// workspace and leaves the one the process holds alone.
func TestJobGraphOpensAnotherCheckoutInItsOwnScope(t *testing.T) {
	process, other := t.TempDir(), t.TempDir()
	for _, dir := range []string{process, other} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), nil, 0o644))
	}
	opened := magusOpened
	t.Cleanup(func() { magusOpened = opened })
	magusOpened = openedRoot{override: process, root: process}
	magusOnce.Do(func() {})

	graphIn := graphsByTree(process, newJobGraph(process, true), true)
	graph := graphIn(other)

	require.NotPanics(t, func() { graph.staleIndexes(t.Context()) })
	require.NotPanics(t, func() { graph.loadScoped(nil)(t.Context()) })
	require.NotPanics(t, func() { graph.LoadDomain(t.Context(), nil) })
	assert.Equal(t, openedRoot{override: process, root: process}, magusOpened)
}
