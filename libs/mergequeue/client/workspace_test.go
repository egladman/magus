package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue"
)

func TestWorkspaceAnswersFromTheProjectGraph(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		"magusfile.buzz": "", "api/magusfile.buzz": "", "web/magusfile.buzz": "", "api/main.go": "package main\n",
	} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}
	w, err := OpenWorkspace(t.Context(), root, "ci")
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	c := mergequeue.Change{ID: "1"}

	affected, unboundedBy, err := w.Affected(t.Context(), c, []string{"api/main.go"})
	require.NoError(t, err)
	assert.Equal(t, []string{"api"}, affected)
	assert.Empty(t, unboundedBy, "a source edit is a proof")

	_, unboundedBy, err = w.Affected(t.Context(), c, []string{"api/main.go", "api/magusfile.buzz"})
	require.NoError(t, err)
	assert.Contains(t, unboundedBy, "api/magusfile.buzz", "an edit to the declarations is not")

	affected, unboundedBy, err = w.Affected(t.Context(), c, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{}, affected, "a change touching nothing reaches nothing")
	assert.Empty(t, unboundedBy)
}

func TestOpenWorkspaceNeedsATarget(t *testing.T) {
	_, err := OpenWorkspace(t.Context(), t.TempDir(), "")
	require.Error(t, err)
}
