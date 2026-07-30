package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shorthandFileName is what installShorthandCmd writes on this platform.
func shorthandFileName() string {
	if runtime.GOOS == "windows" {
		return shorthandName + ".exe"
	}
	return shorthandName
}

func TestInstallShorthand(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, installShorthandCmd([]string{"--dir", dir}))

	target, err := runningBinaryPath()
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(filepath.Join(dir, shorthandFileName()))
	require.NoError(t, err)
	assert.Equal(t, target, resolved)
}

func TestInstallShorthandIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, installShorthandCmd([]string{"--dir", dir}))
	assert.NoError(t, installShorthandCmd([]string{"--dir", dir}))
}

func TestInstallShorthandRefusesToClobberWithoutForce(t *testing.T) {
	dir := t.TempDir()
	occupied := filepath.Join(dir, shorthandFileName())
	require.NoError(t, os.WriteFile(occupied, []byte("not magus"), 0o755))

	assert.ErrorContains(t, installShorthandCmd([]string{"--dir", dir}), "already exists")
	content, err := os.ReadFile(occupied)
	require.NoError(t, err)
	assert.Equal(t, "not magus", string(content))

	require.NoError(t, installShorthandCmd([]string{"--dir", dir, "--force"}))
	target, err := runningBinaryPath()
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(occupied)
	require.NoError(t, err)
	assert.Equal(t, target, resolved)
}

func TestInstallShorthandRejectsUnknownInput(t *testing.T) {
	assert.ErrorContains(t, installShorthandCmd([]string{"extra"}), `unexpected argument "extra"`)
}
