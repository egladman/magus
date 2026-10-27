package image

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePlatformRoundTrips(t *testing.T) {
	for _, s := range []string{"linux/amd64", "linux/arm64", "linux/arm64/v8"} {
		p, err := ParsePlatform(s)
		require.NoError(t, err, s)
		assert.Equal(t, s, p.String())
		text, err := p.MarshalText()
		require.NoError(t, err)
		var back Platform
		require.NoError(t, back.UnmarshalText(text))
		assert.Equal(t, p, back)
	}
	for _, s := range []string{"", "linux", "windows/amd64", "linux/riscv64", "linux/amd64/v2", "a/b/c/d"} {
		_, err := ParsePlatform(s)
		require.Error(t, err, s)
	}
	_, err := Platform{}.MarshalText()
	require.Error(t, err, "the zero platform is not linux/amd64")
}

func TestNormalizeMakesArm64AndV8Equal(t *testing.T) {
	assert.Equal(t, Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}.Normalize(), Platform{OS: "linux", Architecture: "arm64"}.Normalize())
	assert.Equal(t, amd64, amd64.Normalize())
}

func TestParsePlatformsRefusesADuplicate(t *testing.T) {
	ps, err := ParsePlatforms("linux/amd64", "linux/arm64")
	require.NoError(t, err)
	assert.Len(t, ps, 2)
	_, err = ParsePlatforms("linux/arm64", "linux/arm64/v8")
	require.ErrorContains(t, err, "same platform")
}

func TestGoEnv(t *testing.T) {
	env, err := Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}.GoEnv()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"GOOS": "linux", "GOARCH": "arm64"}, env)
	_, err = Platform{}.GoEnv()
	require.Error(t, err)
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{
		"0755": 0o755, "755": 0o755, "1777": 0o1777, "rwxr-xr-x": 0o755, "rw-r--r--": 0o644,
		"rwsr-xr-x": 0o4755, "rwxrwxrwt": 0o1777, "rw-r--r-T": 0o1644,
	} {
		got, err := ParseMode(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"0", "0000", "---------", "10000", "rwxr-xr-q", "0x1ed", ""} {
		_, err := ParseMode(in)
		require.Error(t, err, in)
	}
	assert.Equal(t, "0755", ModeExecutable.String())
	var m Mode
	require.NoError(t, m.UnmarshalText([]byte("rw-------")))
	assert.Equal(t, ModePrivate, m)
}

func TestParseDigestTakesSHA256Only(t *testing.T) {
	_, err := ParseDigest("sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	for _, s := range []string{"", "latest", "0123", "sha512:" + "00"} {
		_, err := ParseDigest(s)
		require.Error(t, err, s)
	}
}
