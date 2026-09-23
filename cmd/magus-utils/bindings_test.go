package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// hostmodules is the union of std's self-registered modules (import
	// triggers their init registration) and std/encoding's explicitly
	// aggregated ones; see its doc.
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

func TestTitleCaseAndRegisterName(t *testing.T) {
	assert.Equal(t, "Fs", titleCase("fs"))
	assert.Equal(t, "Json", titleCase("json"))
	assert.Equal(t, "", titleCase(""))
	assert.Equal(t, "HTTP", titleCase("HTTP"), "an already-capitalized name is left alone")
	assert.Equal(t, "1st", titleCase("1st"), "a non-letter first byte is left alone")
	assert.Equal(t, "RegisterVcs", registerName("vcs"))
}

func TestGoLiteral(t *testing.T) {
	assert.Equal(t, "nil", goLiteral(nil))
	assert.Equal(t, `"x"`, goLiteral("x"))
	assert.Equal(t, `"a\"b"`, goLiteral(`a"b`))
	assert.Equal(t, "3", goLiteral(3))
	assert.Equal(t, "3", goLiteral(int64(3)))
	assert.Equal(t, "1.5", goLiteral(1.5))
	assert.Equal(t, "true", goLiteral(true))
	assert.Equal(t, "false", goLiteral(false))
	// Anything else falls through to %#v, which is Go syntax for the value.
	assert.Equal(t, "[]int{1}", goLiteral([]int{1}))
}

func TestRunBindings(t *testing.T) {
	t.Run("missing flags", func(t *testing.T) {
		err := runBindings(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "usage:")
	})

	t.Run("unknown module", func(t *testing.T) {
		err := runBindings([]string{"-module", "nosuchmodule", "-out", filepath.Join(t.TempDir(), "x.go")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown module")
	})

	t.Run("unknown lang", func(t *testing.T) {
		err := runBindings([]string{"-module", "fs", "-lang", "rust", "-out", filepath.Join(t.TempDir(), "x.go")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown lang")
	})

	t.Run("writes what emitBuzz renders", func(t *testing.T) {
		m, ok := hostmodules.Get("fs")
		require.True(t, ok)
		want, err := emitBuzz(m)
		require.NoError(t, err)

		got := readGenerated(t, runBindings, func(out string) []string {
			return []string{"-module", "fs", "-lang", "buzz", "-out", out}
		})
		assert.Equal(t, string(want), got)
	})
}
