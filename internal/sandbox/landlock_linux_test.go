package sandbox

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/egladman/magus/internal/sandbox/filesystem"
)

// requireLandlock skips a test that needs the kernel sandbox where there is none,
// and fails it instead under MAGUS_TEST_REQUIRE_LANDLOCK=1, so a host that must
// have landlock cannot pass by skipping.
func requireLandlock(t *testing.T) {
	t.Helper()
	if _, err := ABI(); err != nil {
		if os.Getenv("MAGUS_TEST_REQUIRE_LANDLOCK") == "1" {
			t.Fatalf("landlock is required here and unavailable: %v", err)
		}
		t.Skipf("landlock unavailable: %v", err)
	}
}

// TestAccessForPathTypeDropsDirRightsOnFiles pins the masking rule that keeps
// landlock_add_rule from returning EINVAL. Directory-only rights on a regular file are
// invalid, and an allowlist entry naming a file (a resolv.conf, a socket, a config) is
// ordinary; it took down every sandboxed run on a systemd host before this masked.
func TestAccessForPathTypeDropsDirRightsOnFiles(t *testing.T) {
	both := fsAccessReadOnly | unix.LANDLOCK_ACCESS_FS_WRITE_FILE

	kept := accessForPathType(both, true)
	assert.Equal(t, both, kept, "a directory holds every requested right")

	masked := accessForPathType(both, false)
	assert.Equal(t, uint64(0), masked&^fsAccessFile, "no directory-only right survives on a file")
	assert.NotZero(t, masked&unix.LANDLOCK_ACCESS_FS_READ_FILE, "reading the file is still allowed")
	assert.NotZero(t, masked&unix.LANDLOCK_ACCESS_FS_WRITE_FILE, "writing it is still allowed")
}

// TestAccessForPathTypeDropsReferFromAWriteGrant pins the rw("/dev/null") case: a write
// grant carries REFER, which a device cannot hold.
func TestAccessForPathTypeDropsReferFromAWriteGrant(t *testing.T) {
	assert.Equal(t,
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE|unix.LANDLOCK_ACCESS_FS_TRUNCATE|unix.LANDLOCK_ACCESS_FS_IOCTL_DEV,
		accessForPathType(fsAccessWrite, false))
}

// TestRulesetAcceptsAWritableDevice asks the running kernel for the grant every
// sandboxed run makes, read and write on /dev/null, at the highest ABI it offers.
func TestRulesetAcceptsAWritableDevice(t *testing.T) {
	requireLandlock(t)
	abi, err := ABI()
	require.NoError(t, err)
	fd, err := buildRuleset([]filesystem.Rule{{Path: "/dev/null", Read: true, Write: true}}, abi, 0)
	require.NoError(t, err)
	require.NoError(t, unix.Close(fd))
}

// TestAccessForPathTypeCanEmptyTheMask covers the case addPathRule then skips: a rule
// asking ONLY for directory rights leaves nothing to request on a file.
func TestAccessForPathTypeCanEmptyTheMask(t *testing.T) {
	assert.Equal(t, uint64(0), accessForPathType(unix.LANDLOCK_ACCESS_FS_READ_DIR, false))
}

// TestHandledRightsFollowTheABI pins which right each ABI adds, since asking a
// kernel for one it predates fails the whole ruleset with EINVAL.
func TestHandledRightsFollowTheABI(t *testing.T) {
	cases := []struct {
		abi    int
		fs     uint64
		scopes uint64
	}{
		{1, fsAccessV1, 0},
		{2, fsAccessV1 | unix.LANDLOCK_ACCESS_FS_REFER, 0},
		{3, fsAccessV1 | unix.LANDLOCK_ACCESS_FS_REFER | unix.LANDLOCK_ACCESS_FS_TRUNCATE, 0},
		{4, fsAccessV1 | unix.LANDLOCK_ACCESS_FS_REFER | unix.LANDLOCK_ACCESS_FS_TRUNCATE, 0},
		{5, fsAccessV1 | unix.LANDLOCK_ACCESS_FS_REFER | unix.LANDLOCK_ACCESS_FS_TRUNCATE | unix.LANDLOCK_ACCESS_FS_IOCTL_DEV, 0},
		{6, fsAccessV1 | unix.LANDLOCK_ACCESS_FS_REFER | unix.LANDLOCK_ACCESS_FS_TRUNCATE | unix.LANDLOCK_ACCESS_FS_IOCTL_DEV,
			unix.LANDLOCK_SCOPE_SIGNAL | unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET},
	}
	for _, c := range cases {
		assert.Equal(t, c.fs, handledAccessFS(c.abi), "filesystem rights at ABI %d", c.abi)
		assert.Equal(t, c.scopes, handledScopes(c.abi), "scopes at ABI %d", c.abi)
	}
}

func TestABIReportsAVersion(t *testing.T) {
	requireLandlock(t)
	abi, err := ABI()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, abi, 1)
	t.Logf("landlock ABI %d", abi)
}
