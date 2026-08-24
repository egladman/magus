package std

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUUIDVersions pins the two identifier shapes apart. The version nibble at
// index 14 is what a caller reading a stored id uses to tell a random id from a
// time-ordered one, so it is the fact worth asserting rather than the length.
func TestUUIDVersions(t *testing.T) {
	ctx := context.Background()

	v4, err := UUIDv4(ctx)
	require.NoError(t, err)
	assert.Len(t, v4, 36)
	assert.Equal(t, byte('4'), v4[14], "v4 is the random version")

	v7, err := UUIDv7(ctx)
	require.NoError(t, err)
	assert.Len(t, v7, 36)
	assert.Equal(t, byte('7'), v7[14], "v7 is the time-ordered version")

	other, err := UUIDv4(ctx)
	require.NoError(t, err)
	assert.NotEqual(t, v4, other, "two ids must never collide")
}

// TestUUIDv7SortsByCreationTime is the property that makes v7 a usable run id:
// two ids minted a millisecond apart compare in the order they were minted.
func TestUUIDv7SortsByCreationTime(t *testing.T) {
	ctx := context.Background()
	first, err := UUIDv7(ctx)
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	second, err := UUIDv7(ctx)
	require.NoError(t, err)
	assert.Less(t, first, second, "a later v7 must sort after an earlier one")
}

func TestUUIDRandomHex(t *testing.T) {
	ctx := context.Background()

	got, err := UUIDRandomHex(ctx, 8)
	require.NoError(t, err)
	assert.Len(t, got, 16, "n bytes render as 2n hex characters")
	assert.Equal(t, strings.ToLower(got), got, "the hex is lowercase")
	_, decodeErr := hex.DecodeString(got)
	assert.NoError(t, decodeErr)

	other, err := UUIDRandomHex(ctx, 8)
	require.NoError(t, err)
	assert.NotEqual(t, got, other)

	for _, n := range []int{0, -1} {
		_, err := UUIDRandomHex(ctx, n)
		require.Errorf(t, err, "randomHex(%d)", n)
		assert.Contains(t, err.Error(), "n must be positive")
	}
}

func TestUUIDRandomToken(t *testing.T) {
	ctx := context.Background()

	got, err := UUIDRandomToken(ctx, 16)
	require.NoError(t, err)
	assert.NotContains(t, got, "=", "the token is unpadded so it is safe in a URL")
	raw, decodeErr := base64.RawURLEncoding.DecodeString(got)
	require.NoError(t, decodeErr)
	assert.Len(t, raw, 16)

	for _, n := range []int{0, -1} {
		_, err := UUIDRandomToken(ctx, n)
		require.Errorf(t, err, "randomToken(%d)", n)
		assert.Contains(t, err.Error(), "n must be positive")
	}
}

func TestTimeNowISO(t *testing.T) {
	got, err := TimeNowISO(context.Background())
	require.NoError(t, err)

	parsed, parseErr := time.Parse(time.RFC3339, got)
	require.NoError(t, parseErr, "now_iso must render as RFC 3339")
	assert.WithinDuration(t, time.Now(), parsed, time.Minute)
	assert.Equal(t, time.UTC, parsed.Location(), "the clock is anchored to UTC")
}

func TestTimeAdd(t *testing.T) {
	ctx := context.Background()

	got, err := TimeAdd(ctx, 0, "24h")
	require.NoError(t, err)
	assert.InDelta(t, float64(24*60*60*1000), got, 0)

	got, err = TimeAdd(ctx, 10_000, "-1h30m")
	require.NoError(t, err)
	assert.InDelta(t, float64(10_000-90*60*1000), got, 0)

	_, err = TimeAdd(ctx, 0, "not a duration")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "time.add")
}

func TestTimeDiff(t *testing.T) {
	ctx := context.Background()

	got, err := TimeDiff(ctx, 5_000, 2_000)
	require.NoError(t, err)
	assert.InDelta(t, 3_000.0, got, 0)

	got, err = TimeDiff(ctx, 2_000, 5_000)
	require.NoError(t, err)
	assert.InDelta(t, -3_000.0, got, 0, "b later than a reads negative")
}

func TestSemverParse(t *testing.T) {
	ctx := context.Background()

	got, err := SemverParse(ctx, "1.2.3-rc.1+build5")
	require.NoError(t, err)
	assert.Equal(t, types.SemverVersion{
		Major: 1, Minor: 2, Patch: 3,
		Prerelease: "rc.1", Metadata: "build5", Original: "1.2.3-rc.1+build5",
	}, got)

	// Input is lenient and Original keeps the text as written, which is what lets a
	// caller round-trip a tag it read from the VCS.
	got, err = SemverParse(ctx, "v1.2")
	require.NoError(t, err)
	assert.Equal(t, types.SemverVersion{Major: 1, Minor: 2, Original: "v1.2"}, got)

	_, err = SemverParse(ctx, "not-a-version")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "semver.parse")
}

// TestPlatformCPUs reads GOMAXPROCS rather than NumCPU, because inside a container
// with a CPU quota the two disagree and the quota is what bounds the work.
func TestPlatformCPUs(t *testing.T) {
	got, err := PlatformCPUs(context.Background())
	require.NoError(t, err)
	assert.Equal(t, goruntime.GOMAXPROCS(0), got)
	assert.Positive(t, got)
}

// TestPlatformMemory: zero is UNKNOWN, not "no memory", so a caller branches on it
// rather than sizing work off it. Either answer is legitimate here - the host may
// be a platform mem cannot measure.
func TestPlatformMemory(t *testing.T) {
	got, err := PlatformMemory(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, got, 0, "memory_bytes never reports a negative or truncated figure")
}

func TestStringsContains(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		s, substr string
		want      bool
	}{
		{"magusfile", "file", true},
		{"magusfile", "FILE", false},
		{"magusfile", "", true},
		{"", "x", false},
	} {
		got, err := StringsContains(ctx, tc.s, tc.substr)
		require.NoError(t, err)
		assert.Equalf(t, tc.want, got, "contains(%q, %q)", tc.s, tc.substr)
	}
}

// TestCwdHelpers covers the split between the two accessors: EffectiveCwd falls
// back to the process cwd so a host module always has a base, while
// CwdFromContext reports only a cwd a target actually established.
func TestCwdHelpers(t *testing.T) {
	dir := t.TempDir()
	ctx := WithCwd(context.Background(), dir)

	got, err := EffectiveCwd(ctx)
	require.NoError(t, err)
	assert.Equal(t, dir, got)

	base, ok := CwdFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, dir, base)

	base, ok = CwdFromContext(context.Background())
	assert.False(t, ok, "no target established a cwd, so there is none to report")
	assert.Empty(t, base)

	// An empty dir is a no-op rather than an erasure.
	assert.Equal(t, context.Background(), WithCwd(context.Background(), ""))

	process, err := EffectiveCwd(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, process, "without a context cwd the process cwd is the base")
}
