package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedHookNameRequiresTheManagedDirectory(t *testing.T) {
	name, managed := embeddedHookName(filepath.Join(t.TempDir(), ".magus", "harness", "hooks", "magus-command.buzz"))
	require.True(t, managed)
	assert.Equal(t, "magus-command.buzz", name)
	for _, path := range []string{"magus-command.buzz", "hooks/magus-command.buzz", ".magus/harness/hooks/../other.buzz"} {
		_, managed := embeddedHookName(path)
		assert.False(t, managed, path)
	}
}

func TestEmbeddedHookRefusesAnUnknownAssetWithoutReadingInstalledCode(t *testing.T) {
	filename := filepath.Join(t.TempDir(), ".magus", "harness", "hooks", "unknown.buzz")
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
	require.NoError(t, os.WriteFile(filename, []byte("import \"std\"; fun main(args: [str]) > void {}"), 0o644))
	err := runEmbeddedHook(t.Context(), "", interp.ScriptOptions{}, filename, "unknown.buzz", nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "unknown.buzz")
}
