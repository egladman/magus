package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunSpellsRefusesAnEmptySource(t *testing.T) {
	t.Run("no spells dir", func(t *testing.T) {
		err := runSpells([]string{
			"-spells", filepath.Join(t.TempDir(), "absent"),
			"-out", t.TempDir(),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read spells dir")
	})

	// Compiling nothing and exiting 0 is the silent failure this guard exists for:
	// the embedded set would come out empty with nothing saying so.
	t.Run("no built-ins found", func(t *testing.T) {
		err := runSpells([]string{"-spells", t.TempDir(), "-out", t.TempDir()})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no built-in spells found")
	})
}
