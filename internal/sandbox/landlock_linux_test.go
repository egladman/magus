package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/sys/unix"
)

// TestAccessForPathTypeDropsDirRightsOnFiles pins the masking rule that keeps
// landlock_add_rule from returning EINVAL. Directory-only rights on a regular file are
// invalid, and an allowlist entry naming a file (a resolv.conf, a socket, a config) is
// ordinary - it took down every sandboxed run on a systemd host before this masked.
func TestAccessForPathTypeDropsDirRightsOnFiles(t *testing.T) {
	both := fsAccessReadOnly | unix.LANDLOCK_ACCESS_FS_WRITE_FILE

	kept := accessForPathType(both, true)
	assert.Equal(t, both, kept, "a directory holds every requested right")

	masked := accessForPathType(both, false)
	assert.Equal(t, uint64(0), masked&fsAccessDirOnly, "no directory-only right survives on a file")
	assert.NotZero(t, masked&unix.LANDLOCK_ACCESS_FS_READ_FILE, "reading the file is still allowed")
	assert.NotZero(t, masked&unix.LANDLOCK_ACCESS_FS_WRITE_FILE, "writing it is still allowed")
}

// TestAccessForPathTypeCanEmptyTheMask covers the case addPathRule then skips: a rule
// asking ONLY for directory rights leaves nothing to request on a file.
func TestAccessForPathTypeCanEmptyTheMask(t *testing.T) {
	assert.Equal(t, uint64(0), accessForPathType(unix.LANDLOCK_ACCESS_FS_READ_DIR, false))
}
