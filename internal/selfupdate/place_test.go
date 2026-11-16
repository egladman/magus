package selfupdate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlaceIntoGivesTheTargetANewInode(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "new")
	target := filepath.Join(dir, "magus")
	require.NoError(t, os.WriteFile(source, []byte("new body"), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("old body"), 0o755))
	before, err := os.Stat(target)
	require.NoError(t, err)

	require.NoError(t, PlaceInto(source, target))

	body, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "new body", string(body))
	after, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), after.Mode().Perm())
	assert.NotEqual(t, inode(before), inode(after), "a replaced binary must be a new file, or macOS kills it")
}

func inode(fi os.FileInfo) uint64 {
	return fi.Sys().(*syscall.Stat_t).Ino
}

// stampScript writes an executable that prints stamp as `version -o json` would.
func stampScript(t *testing.T, stamp string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "candidate.sh")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho '"+stamp+"'\n"), 0o755))
	return path
}

func TestPlaceRefusesAReplacementWithoutACleanStamp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the candidates are POSIX shell scripts")
	}
	dirty := stampScript(t, `{"commit":"abc","dirty":true,"build_date":"2099-01-01T00:00:00Z"}`)
	unstamped := stampScript(t, `{"commit":"","dirty":false,"build_date":""}`)

	_, err := Place(context.Background(), dirty)
	assert.ErrorContains(t, err, "built from a modified tree")

	_, err = Place(context.Background(), unstamped)
	assert.ErrorContains(t, err, "reports no commit or build date")
}

func TestReadPlaceStampNamesTheSourceWhenItCannotReportAVersion(t *testing.T) {
	_, err := ReadPlaceStamp(context.Background(), filepath.Join(t.TempDir(), "absent"))
	assert.ErrorContains(t, err, "did not report its version")
}

// shorthandFileName is what InstallShorthand writes on this platform.
func shorthandFileName() string {
	if runtime.GOOS == "windows" {
		return ShorthandName + ".exe"
	}
	return ShorthandName
}

func TestInstallShorthand(t *testing.T) {
	dir := t.TempDir()
	got, err := InstallShorthand(dir, false)
	require.NoError(t, err)
	assert.True(t, got.Created)

	target, err := RunningBinaryPath()
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(filepath.Join(dir, shorthandFileName()))
	require.NoError(t, err)
	assert.Equal(t, target, resolved)
}

func TestInstallShorthandIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	_, err := InstallShorthand(dir, false)
	require.NoError(t, err)
	again, err := InstallShorthand(dir, false)
	require.NoError(t, err)
	assert.False(t, again.Created)
}

func TestInstallShorthandRefusesToClobberWithoutForce(t *testing.T) {
	w := testkit.NewWorkspace(t)
	dir := w.Root()
	occupied := w.WriteExecutable(shorthandFileName(), "not magus")

	_, err := InstallShorthand(dir, false)
	assert.ErrorContains(t, err, "already exists")
	content, err := os.ReadFile(occupied)
	require.NoError(t, err)
	assert.Equal(t, "not magus", string(content))

	_, err = InstallShorthand(dir, true)
	require.NoError(t, err)
	target, err := RunningBinaryPath()
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(occupied)
	require.NoError(t, err)
	assert.Equal(t, target, resolved)
}
