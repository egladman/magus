//go:build !windows

package proc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVerifySockDirRejectsWorldWritable guards S-2: os.MkdirAll(dir, 0o700) is
// a silent no-op when dir already exists, so it neither chmods nor checks
// ownership of a directory another local user pre-created. The fallback
// $TMPDIR/magus-$UID sits under a world-writable parent with a fully
// predictable name (UIDs are enumerable), so a loosely permissioned
// pre-created directory is exactly what an attacker would leave behind.
func TestVerifySockDirRejectsWorldWritable(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o777))

	err := verifySockDir(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "group/other accessible")
}

// TestVerifySockDirAcceptsPrivateOwnDir is the normal case that must keep
// working: a directory this or an earlier magus version created with
// MkdirAll(dir, 0o700), owned by us. Rejecting this would stop magus from
// starting for anyone with a pre-existing socket directory.
func TestVerifySockDirAcceptsPrivateOwnDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o700))

	assert.NoError(t, verifySockDir(dir))
}

// TestVerifySockDirRejectsSymlink covers the other attacker move: standing a
// symlink in for the directory so the socket ends up wherever the link
// points.
func TestVerifySockDirRejectsSymlink(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "real")
	require.NoError(t, os.Mkdir(target, 0o700))
	link := filepath.Join(parent, "link")
	require.NoError(t, os.Symlink(target, link))

	err := verifySockDir(link)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")
}

// TestVerifySockDirMissingIsNotAnError: a directory that does not exist yet
// (MkdirAll failed upstream, e.g. a read-only TMPDIR) is not this function's
// problem to report - the bind that follows fails safely on its own, exactly
// as it did before this check existed.
func TestVerifySockDirMissingIsNotAnError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	assert.NoError(t, verifySockDir(dir))
}

// The wrong-owner case (a directory owned by a different uid) is not covered
// here: creating one requires privileges this test process does not have, and
// faking os.Lstat's Sys() result would not exercise the real syscall.Stat_t
// path this check relies on.
