package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestManifestUpToDate is the gate the manifest lacked. It had drifted by eleven
// modules - base64, csv, hex, ini, url, log, math, net, sort, term, diff were all
// absent - so editor completion and hover silently did not know they existed. A
// snapshot with no gate is a snapshot that rots, and this one rotted unnoticed
// across a whole stdlib expansion.
func TestManifestUpToDate(t *testing.T) {
	want, _, err := render()
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.FromSlash("../../" + outFile))
	require.NoError(t, err, "read the committed manifest")

	assert.Equal(t, string(want), string(got),
		"%s is out of date; regenerate with: go run ./cmd/langservice-manifest", outFile)
}
