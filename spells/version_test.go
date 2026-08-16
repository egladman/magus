package spells

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
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

// The default extracts nothing: guessing which number in a tool's output is its
// version is a guess magus does not make unless a spell asks.
func TestVersionTokenDefaultKeepsWholeOutput(t *testing.T) {
	tok, note := VersionToken("Docker version 29.3.1, build c2be9cc", VersionKey{})
	assert.Equal(t, "Docker version 29.3.1, build c2be9cc", tok)
	assert.Empty(t, note)
}

// Asking for patch is how a spell sheds the build identity tools pad a version line
// with, without narrowing anything a team has to reason about.
func TestVersionTokenPatchShedsBuildIdentity(t *testing.T) {
	key := VersionKey{UpTo: VersionPatch}
	a, _ := VersionToken("Docker version 29.3.1, build c2be9cc", key)
	b, _ := VersionToken("Docker version 29.3.1, build 0000000", key)
	assert.Equal(t, "v29.3.1", a)
	assert.Equal(t, a, b)
}

// The whole-output fallback: a tool magus cannot parse keys on everything it printed,
// which is what magus did for every tool before extraction existed.
func TestVersionTokenUnparseableFallsBackToRawOutput(t *testing.T) {
	tok, note := VersionToken("  some opaque build id  ", VersionKey{UpTo: VersionMajor})
	assert.Equal(t, "some opaque build id", tok)
	assert.Contains(t, note, "no semver-shaped token")
}

// Truncation groups versions exactly as a "same major/minor" comparison would, which
// is the whole claim: each case pairs a version with another that must share its token.
func TestVersionTokenKeyOn(t *testing.T) {
	for _, tc := range []struct {
		upTo VersionComponent
		out  string
		want string
		same string
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
	assert.NotEqual(t, dp, df, "the whole-output default must distinguish them")
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

// The real `govulncheck -version` output. Its shape is why Verbatim exists: the FIRST
// semver-shaped token is the Go version, not the scanner's, and the database date -
// the field that decides whether a verdict still holds - is not semver-shaped at all.
const govulncheckVersion = `Go: go1.21.0
Scanner: govulncheck@v1.0.1
DB: https://vuln.go.dev
DB updated: 2023-08-23 14:38:50 +0000 UTC`

// Extraction picks the wrong number here, which is the bug Verbatim exists to avoid.
func TestExtractVersionPicksGoVersionFromGovulncheck(t *testing.T) {
	got, ok := ExtractVersion(govulncheckVersion)
	require.True(t, ok)
	assert.Equal(t, "v1.21.0", got, "first-match extraction takes the Go version, not the scanner's")
}

// The regression this guards: a vulnerability-database update must move the cache key,
// or a newly published CVE leaves every cached pass replaying unchanged.
func TestVerbatimKeepsVulnerabilityDatabaseDate(t *testing.T) {
	updated := `Go: go1.21.0
Scanner: govulncheck@v1.0.1
DB: https://vuln.go.dev
DB updated: 2026-08-05 09:00:00 +0000 UTC`

	// The DEFAULT, not an opt-out: govulncheck declares nothing and is correct.
	a, noteA := VersionToken(govulncheckVersion, VersionKey{})
	b, noteB := VersionToken(updated, VersionKey{})
	assert.Empty(t, noteA)
	assert.Empty(t, noteB)
	assert.NotEqual(t, a, b, "a database update must move the cache key")
	assert.Contains(t, a, "DB updated")

	// Had extraction been the default, both would collapse to the Go version and the
	// database update would vanish. This equality is the bug the default avoids.
	c, _ := VersionToken(govulncheckVersion, VersionKey{UpTo: VersionPatch})
	d, _ := VersionToken(updated, VersionKey{UpTo: VersionPatch})
	assert.Equal(t, c, d)
}

// Const outranks everything: no process is spawned, so there is no output to read.
func TestConstOutranksUpTo(t *testing.T) {
	tok, note := VersionToken("mytool 1.2.3", VersionKey{Const: "pinned-1", UpTo: VersionMajor})
	assert.Equal(t, "pinned-1", tok)
	assert.Empty(t, note)
}

// The versions here are the shape ExtractVersion emits (canonical "vX.Y.Z") and the
// bounds are the shape an author writes (no "v", often partial). That mismatch is the
// whole reason normalizeBound exists, so the table exercises it rather than pre-
// normalizing both sides.
func TestVersionBoundsCheck(t *testing.T) {
	for _, tc := range []struct {
		name    string
		bounds  VersionBounds
		version string
		want    Verdict
	}{
		{"no bounds accepts anything", VersionBounds{}, "v1.0.0", VerdictInside},
		{"above the floor", VersionBounds{Min: "1.21"}, "v1.26.5", VerdictInside},
		{"exactly the floor", VersionBounds{Min: "1.21"}, "v1.21.0", VerdictInside},
		{"partial floor means dot zero", VersionBounds{Min: "1.21"}, "v1.21.4", VerdictInside},
		{"below the floor", VersionBounds{Min: "2.0"}, "v1.4.2", VerdictTooOld},

		// below is the first version REJECTED. An inclusive max would make the second
		// case pass and the third fail, which is the off-by-one the name exists to stop.
		{"under the ceiling", VersionBounds{Below: "25"}, "v24.19.0", VerdictInside},
		{"exactly the ceiling", VersionBounds{Below: "25"}, "v25.0.0", VerdictTooNew},
		{"over the ceiling", VersionBounds{Below: "25"}, "v25.9.0", VerdictTooNew},

		{"inside both", VersionBounds{Min: "22", Below: "25"}, "v24.19.0", VerdictInside},
		{"under both", VersionBounds{Min: "22", Below: "25"}, "v20.1.0", VerdictTooOld},
		{"over both", VersionBounds{Min: "22", Below: "25"}, "v26.0.0", VerdictTooNew},

		// Unknown is never a violation. A window magus cannot evaluate must not fail a
		// build; the alternative is a typo silently blocking every op that uses the tool.
		{"unparseable version", VersionBounds{Min: "1.0"}, "not-a-version", VerdictUnknown},
		{"unparseable floor", VersionBounds{Min: "latest"}, "v1.0.0", VerdictUnknown},
		{"unparseable ceiling", VersionBounds{Below: "next"}, "v1.0.0", VerdictUnknown},

		// A prerelease sorts below its release, so a ceiling of 25 rejects 25.0.0-rc1
		// and a floor of 25 does too. Both follow from semver ordering rather than from
		// anything this type decides.
		{"prerelease under the ceiling is still the ceiling line", VersionBounds{Below: "25"}, "v25.0.0-rc1", VerdictInside},
		{"prerelease below the floor", VersionBounds{Min: "25"}, "v25.0.0-rc1", VerdictTooOld},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.bounds.Check(tc.version))
		})
	}
}

// Intersect is what stops a spell and a workspace from loosening each other. Narrower
// wins on each bound independently, so a workspace that sets only a ceiling keeps the
// spell's floor rather than replacing the whole window.
func TestVersionBoundsIntersect(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spell VersionBounds
		works VersionBounds
		want  VersionBounds
	}{
		{"workspace adds a ceiling to a spell floor",
			VersionBounds{Min: "1.21"}, VersionBounds{Below: "1.28"},
			VersionBounds{Min: "1.21", Below: "1.28"}},
		{"workspace raises the floor",
			VersionBounds{Min: "1.21"}, VersionBounds{Min: "1.26"},
			VersionBounds{Min: "1.26"}},
		{"workspace cannot lower the floor",
			VersionBounds{Min: "1.26"}, VersionBounds{Min: "1.21"},
			VersionBounds{Min: "1.26"}},
		{"workspace cannot raise the ceiling",
			VersionBounds{Below: "25"}, VersionBounds{Below: "30"},
			VersionBounds{Below: "25"}},
		{"empty workspace changes nothing",
			VersionBounds{Min: "1.21", Below: "2"}, VersionBounds{},
			VersionBounds{Min: "1.21", Below: "2"}},
		{"empty spell takes the workspace whole",
			VersionBounds{}, VersionBounds{Min: "22", Below: "25"},
			VersionBounds{Min: "22", Below: "25"}},
		// Keeping the unparseable bound is what surfaces it: dropping it would widen
		// the window silently, where Check turns it into VerdictUnknown a reader sees.
		{"an unparseable candidate never wins",
			VersionBounds{Min: "1.21"}, VersionBounds{Min: "latest"},
			VersionBounds{Min: "1.21"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.spell.Intersect(tc.works))
		})
	}
}

func TestVersionBoundsIsZero(t *testing.T) {
	assert.True(t, VersionBounds{}.IsZero())
	assert.False(t, VersionBounds{Min: "1"}.IsZero())
	assert.False(t, VersionBounds{Below: "2"}.IsZero())
}
