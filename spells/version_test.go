package spells

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures are REAL output, captured from the tools magus ships probes for, not
// invented strings. That is the point of the test: the extractor's job is to survive
// what maintainers actually print, and every one of these wraps the version in prose
// that a hand-written parser would have to special-case.
func TestExtractVersionRealProbeOutput(t *testing.T) {
	for _, tc := range []struct {
		tool string
		out  string
		want string
	}{
		{"go", "go version go1.26.0 linux/amd64", "v1.26.0"},
		{"golangci-lint", "golangci-lint has version 2.5.0 built with go1.25.1 from ff63786c on 2025-09-21T19:04:05Z", "v2.5.0"},
		{"node", "v22.22.2", "v22.22.2"},
		{"python3", "Python 3.11.15", "v3.11.15"},
		{"docker", "Docker version 29.3.1, build c2be9cc", "v29.3.1"},
		{"bash", "GNU bash, version 5.2.21(1)-release (x86_64-pc-linux-gnu)", "v5.2.21"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			got, ok := ExtractVersion(tc.out)
			require.True(t, ok, "no version extracted from %q", tc.out)
			assert.Equal(t, tc.want, got)
		})
	}
}

// The determinism claim, stated as a test: the fields that made two machines running
// the same tool compute different cache keys must not survive extraction.
func TestExtractVersionDropsBuildIdentity(t *testing.T) {
	// Same golangci-lint version, different build: another commit, another timestamp,
	// built with another Go. Only the version is the tool's identity.
	a, ok := ExtractVersion("golangci-lint has version 2.5.0 built with go1.25.1 from ff63786c on 2025-09-21T19:04:05Z")
	require.True(t, ok)
	b, ok := ExtractVersion("golangci-lint has version 2.5.0 built with go1.24.0 from deadbeef on 2024-01-02T03:04:05Z")
	require.True(t, ok)
	assert.Equal(t, a, b, "build identity leaked into the extracted version")

	// Same docker version, different build hash.
	c, ok := ExtractVersion("Docker version 29.3.1, build c2be9cc")
	require.True(t, ok)
	d, ok := ExtractVersion("Docker version 29.3.1, build 0000000")
	require.True(t, ok)
	assert.Equal(t, c, d)

	// Same go version, different host platform. The platform still keys the cache -
	// see internal/cache/hash.go's platform line - just not through the spell.
	e, ok := ExtractVersion("go version go1.26.0 linux/amd64")
	require.True(t, ok)
	f, ok := ExtractVersion("go version go1.26.0 darwin/arm64")
	require.True(t, ok)
	assert.Equal(t, e, f)
}

func TestExtractVersionMissing(t *testing.T) {
	for _, out := range []string{"", "no version here", "build 7", "x86_64-pc-linux-gnu"} {
		_, ok := ExtractVersion(out)
		assert.False(t, ok, "unexpectedly extracted a version from %q", out)
	}
}

func TestExtractVersionPrerelease(t *testing.T) {
	got, ok := ExtractVersion("mytool 1.2.3-rc1 (abc)")
	require.True(t, ok)
	assert.Equal(t, "v1.2.3-rc1", got)
}

// A two-component version is completed to X.Y.0 so it parses as semver downstream.
func TestExtractVersionTwoComponent(t *testing.T) {
	got, ok := ExtractVersion("shellcheck 0.9")
	require.True(t, ok)
	assert.Equal(t, "v0.9.0", got)
}

func TestVersionTokenAuthorSuppliedVersionSkipsTheProbe(t *testing.T) {
	tok, note := VersionToken("ignored entirely", VersionKey{Const: "protoc-gen-go-1"})
	assert.Equal(t, "protoc-gen-go-1", tok)
	assert.Empty(t, note)
}

func TestVersionTokenDefaultKeepsWholeVersion(t *testing.T) {
	tok, note := VersionToken("Docker version 29.3.1, build c2be9cc", VersionKey{})
	assert.Equal(t, "v29.3.1", tok)
	assert.Empty(t, note)
}

// The whole-output fallback: a tool magus cannot parse keys on everything it printed,
// which is what magus did for every tool before extraction existed.
func TestVersionTokenUnparseableFallsBackToRawOutput(t *testing.T) {
	tok, note := VersionToken("  some opaque build id  ", VersionKey{})
	assert.Equal(t, "some opaque build id", tok)
	assert.Contains(t, note, "no semver-shaped token")
}

// Truncation groups versions exactly as a "same major/minor" comparison would, which
// is the whole claim: each case pairs a version with another that must share its token.
func TestVersionTokenKeyOn(t *testing.T) {
	for _, tc := range []struct {
		upTo  VersionComponent
		out   string
		want  string
		same  string
	}{
		{VersionMajor, "golangci-lint has version 2.5.0 built with go1.25.1", "v2", "golangci-lint has version 2.9.4 built with go1.24.0"},
		{VersionMinor, "go version go1.26.0 linux/amd64", "v1.26", "go version go1.26.7 darwin/arm64"},
		{VersionPatch, "Python 3.11.15", "v3.11.15", "Python 3.11.15"},
	} {
		t.Run(string(tc.upTo), func(t *testing.T) {
			policy := VersionKey{UpTo: tc.upTo}
			tok, note := VersionToken(tc.out, policy)
			assert.Equal(t, tc.want, tok)
			assert.Empty(t, note)

			same, _ := VersionToken(tc.same, policy)
			assert.Equal(t, tok, same, "versions that should share a token did not")
		})
	}
}

// A different major must NOT share a major-keyed token, or the policy would collapse
// everything into one entry.
func TestVersionTokenKeyOnSeparatesDifferentComponents(t *testing.T) {
	major := VersionKey{UpTo: VersionMajor}
	a, _ := VersionToken("mytool 2.5.0", major)
	b, _ := VersionToken("mytool 3.0.0", major)
	assert.NotEqual(t, a, b)

	minor := VersionKey{UpTo: VersionMinor}
	c, _ := VersionToken("mytool 2.5.0", minor)
	d, _ := VersionToken("mytool 2.6.0", minor)
	assert.NotEqual(t, c, d)
}

// KeyOn patch drops the prerelease; the empty default keeps it. That difference is the
// only thing distinguishing the two, so it gets a test.
func TestVersionTokenPatchDropsPrereleaseButDefaultKeepsIt(t *testing.T) {
	patch, _ := VersionToken("mytool 1.2.3-rc1", VersionKey{UpTo: VersionPatch})
	final, _ := VersionToken("mytool 1.2.3", VersionKey{UpTo: VersionPatch})
	assert.Equal(t, "v1.2.3", patch)
	assert.Equal(t, patch, final, "UpTo patch must ignore the prerelease")

	dp, _ := VersionToken("mytool 1.2.3-rc1", VersionKey{})
	df, _ := VersionToken("mytool 1.2.3", VersionKey{})
	assert.NotEqual(t, dp, df, "the default must distinguish a prerelease from the release")
}

func TestVersionTokenUnknownKeyOnDegrades(t *testing.T) {
	tok, note := VersionToken("mytool 1.2.3", VersionKey{UpTo: VersionComponent("decade")})
	assert.Equal(t, "v1.2.3", tok)
	assert.Contains(t, note, "unknown version component")
}

func TestVersionKeyIsZero(t *testing.T) {
	assert.True(t, VersionKey{}.IsZero())
	assert.False(t, VersionKey{Const: "x"}.IsZero())
	assert.False(t, VersionKey{UpTo: VersionMajor}.IsZero())
}
