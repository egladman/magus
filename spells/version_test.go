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
		{"go", "go version go1.26.0 linux/amd64", "1.26.0"},
		{"golangci-lint", "golangci-lint has version 2.5.0 built with go1.25.1 from ff63786c on 2025-09-21T19:04:05Z", "2.5.0"},
		{"node", "v22.22.2", "22.22.2"},
		{"python3", "Python 3.11.15", "3.11.15"},
		{"docker", "Docker version 29.3.1, build c2be9cc", "29.3.1"},
		{"bash", "GNU bash, version 5.2.21(1)-release (x86_64-pc-linux-gnu)", "5.2.21"},
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
	assert.Equal(t, "1.2.3-rc1", got)
}

// A two-component version is completed to X.Y.0 so it parses as semver downstream.
func TestExtractVersionTwoComponent(t *testing.T) {
	got, ok := ExtractVersion("shellcheck 0.9")
	require.True(t, ok)
	assert.Equal(t, "0.9.0", got)
}

func TestVersionTokenLiteralSkipsEverything(t *testing.T) {
	tok, note := VersionToken("ignored entirely", VersionPolicy{Literal: "protoc-gen-go-1"})
	assert.Equal(t, "protoc-gen-go-1", tok)
	assert.Empty(t, note)
}

func TestVersionTokenDefaultIsExact(t *testing.T) {
	tok, note := VersionToken("Docker version 29.3.1, build c2be9cc", VersionPolicy{})
	assert.Equal(t, "29.3.1", tok)
	assert.Empty(t, note)
}

// The whole-output fallback: a tool magus cannot parse keys on everything it printed,
// which is what magus did for every tool before extraction existed.
func TestVersionTokenUnparseableFallsBackToRawOutput(t *testing.T) {
	tok, note := VersionToken("  some opaque build id  ", VersionPolicy{})
	assert.Equal(t, "some opaque build id", tok)
	assert.Contains(t, note, "no semver-shaped token")
}

func TestVersionTokenPrecision(t *testing.T) {
	for _, tc := range []struct {
		name  string
		prec  Precision
		out   string
		want  string
		other string // a version that must land in the SAME bucket
	}{
		{"major", PrecisionMajor, "golangci-lint has version 2.5.0 built with go1.25.1", ">= 2.0.0, < 3.0.0", "golangci-lint has version 2.9.4 built with go1.25.1"},
		{"minor", PrecisionMinor, "go version go1.26.0 linux/amd64", ">= 1.26.0, < 1.27.0", "go version go1.26.7 darwin/arm64"},
		{"patch", PrecisionPatch, "Python 3.11.15", ">= 3.11.15, < 3.11.16", "Python 3.11.15"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := VersionPolicy{Buckets: []VersionBucket{{Precision: tc.prec}}}
			tok, note := VersionToken(tc.out, policy)
			assert.Equal(t, tc.want, tok)
			assert.Empty(t, note)

			same, _ := VersionToken(tc.other, policy)
			assert.Equal(t, tok, same, "versions that should share a bucket produced different tokens")
		})
	}
}

// A precision bucket is derived from the probed version, so it contains that version by
// construction - including a prerelease, which a plain semver constraint would exclude.
func TestVersionTokenPrecisionCoversPrerelease(t *testing.T) {
	tok, note := VersionToken("mytool 2.5.0-rc1", VersionPolicy{Buckets: []VersionBucket{{Precision: PrecisionMajor}}})
	assert.Equal(t, ">= 2.0.0, < 3.0.0", tok)
	assert.Empty(t, note)
}

func TestVersionTokenExplicitRangeFirstMatchWins(t *testing.T) {
	policy := VersionPolicy{Buckets: []VersionBucket{
		{Constraint: ">= 1.0.0, < 2.0.0"},
		{Constraint: ">= 2.0.0, < 4.0.0"},
	}}
	tok, note := VersionToken("mytool 2.5.0", policy)
	assert.Equal(t, ">= 2.0.0, < 4.0.0", tok)
	assert.Empty(t, note)

	// 3.9.0 sits in the same declared bucket, so it replays 2.5.0's cache.
	same, _ := VersionToken("mytool 3.9.0", policy)
	assert.Equal(t, tok, same)

	// 1.4.0 is a different bucket.
	other, _ := VersionToken("mytool 1.4.0", policy)
	assert.Equal(t, ">= 1.0.0, < 2.0.0", other)
}

// Degradation, not failure: a version outside every declared bucket keys exactly, so a
// tool upgrade costs a cache miss rather than a red pipeline.
func TestVersionTokenUnmatchedKeysExactly(t *testing.T) {
	policy := VersionPolicy{Buckets: []VersionBucket{{Constraint: ">= 1.0.0, < 2.0.0"}}}
	tok, note := VersionToken("mytool 9.9.9", policy)
	assert.Equal(t, "9.9.9", tok)
	assert.Contains(t, note, "matches none")
}

func TestVersionTokenBadConstraintDegrades(t *testing.T) {
	policy := VersionPolicy{Buckets: []VersionBucket{{Constraint: "not a constraint"}}}
	tok, note := VersionToken("mytool 1.2.3", policy)
	assert.Equal(t, "1.2.3", tok)
	assert.Contains(t, note, "unparseable constraint")
}

func TestVersionTokenBadPrecisionDegrades(t *testing.T) {
	policy := VersionPolicy{Buckets: []VersionBucket{{Precision: Precision("decade")}}}
	tok, note := VersionToken("mytool 1.2.3", policy)
	assert.Equal(t, "1.2.3", tok)
	assert.Contains(t, note, "unknown precision")
}

// An explicit range before a precision catch-all is the shape the docs recommend, so
// it gets a test: the range wins when it matches, the catch-all handles the rest.
func TestVersionTokenRangeThenPrecisionCatchAll(t *testing.T) {
	policy := VersionPolicy{Buckets: []VersionBucket{
		{Constraint: ">= 1.0.0, < 2.0.0"},
		{Precision: PrecisionMajor},
	}}
	in, _ := VersionToken("mytool 1.7.0", policy)
	assert.Equal(t, ">= 1.0.0, < 2.0.0", in)

	out, note := VersionToken("mytool 5.1.0", policy)
	assert.Equal(t, ">= 5.0.0, < 6.0.0", out)
	assert.Empty(t, note, "a precision catch-all should leave nothing to note")
}

func TestVersionPolicyIsZero(t *testing.T) {
	assert.True(t, VersionPolicy{}.IsZero())
	assert.False(t, VersionPolicy{Literal: "x"}.IsZero())
	assert.False(t, VersionPolicy{Buckets: []VersionBucket{{Precision: PrecisionMajor}}}.IsZero())
}
