package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// hostmodules is the union of std's self-registered modules (import
	// triggers their init registration) and std/encoding's explicitly
	// aggregated ones - see its doc.
	"github.com/egladman/magus/internal/hostmodules"
)

// buzzModules lists modules with generated Buzz host bindings.
var buzzModules = []string{
	"os", "vcs", "fs", "archive", "env", "json", "http", "charm",
}

// TestBuzzFilesUpToDate verifies that the checked-in bindings/gen/<module>.go files
// are byte-for-byte identical to what `magus-utils bindings -lang buzz` would emit today.
func TestBuzzFilesUpToDate(t *testing.T) {
	genBuzzDir := filepath.Join("..", "..", "internal", "interp", "bindings", "gen")

	for _, name := range buzzModules {
		t.Run(name, func(t *testing.T) {
			m, ok := hostmodules.Get(name)
			require.True(t, ok, "hostmodules.Get(%q): module not registered", name)
			want, err := emitBuzz(m)
			require.NoError(t, err, "emitBuzz(%q)", name)
			outPath := filepath.Join(genBuzzDir, name+".go")
			got, err := os.ReadFile(outPath)
			require.NoError(t, err, "read %s", outPath)
			assert.Equal(t, string(want), string(got),
				"%s.go is out of date; re-run:\n  go generate ./std/", name)
		})
	}
}

// TestObjectReturnContracts prevents host metadata from advertising a Go struct
// name when the checker receives a differently named generated Buzz object.
func TestObjectReturnContracts(t *testing.T) {
	require.NoError(t, checkObjectDecls(hostmodules.All()))
}
