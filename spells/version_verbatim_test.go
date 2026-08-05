package spells

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	verbatim := VersionKey{Verbatim: true}
	a, noteA := VersionToken(govulncheckVersion, verbatim)
	b, noteB := VersionToken(updated, verbatim)
	assert.Empty(t, noteA)
	assert.Empty(t, noteB)
	assert.NotEqual(t, a, b, "a database update must move the cache key")
	assert.Contains(t, a, "DB updated")

	// Without Verbatim both collapse to the Go version, and the update vanishes.
	c, _ := VersionToken(govulncheckVersion, VersionKey{})
	d, _ := VersionToken(updated, VersionKey{})
	assert.Equal(t, c, d, "this equality IS the bug Verbatim avoids")
}

// Verbatim outranks UpTo rather than silently combining with it.
func TestVerbatimIgnoresUpTo(t *testing.T) {
	tok, note := VersionToken("mytool 1.2.3", VersionKey{Verbatim: true, UpTo: VersionMajor})
	assert.Equal(t, "mytool 1.2.3", tok)
	assert.Empty(t, note)
}

func TestVerbatimIsNotZero(t *testing.T) {
	assert.False(t, VersionKey{Verbatim: true}.IsZero())
}
