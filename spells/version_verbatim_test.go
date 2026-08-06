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
