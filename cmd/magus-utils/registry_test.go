package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// upstreamFixtures holds real captured endoflife.date v1 responses. Real ones,
// because the shape this parses is a third party's and a fixture we wrote
// ourselves would only ever prove we agree with ourselves.
const upstreamFixtures = "testdata/upstream"

const fixedExpiry = "2027-01-01T00:00:00Z"

func TestBuildRegistryFromUpstream(t *testing.T) {
	reg, err := buildRegistry(upstreamFixtures, "", fixedExpiry)
	require.NoError(t, err)

	assert.Equal(t, registry.SchemaVersion, reg.SchemaVersion)
	assert.Equal(t, fixedExpiry, reg.ExpiresAt)
	require.Contains(t, reg.EOL, "nodejs", "keyed by upstream product slug, not by magus bin name")
	require.Contains(t, reg.EOL, "go")
	assert.NotEmpty(t, reg.EOL["nodejs"].Label)
	assert.NotEmpty(t, reg.EOL["nodejs"].Cycles)

	// The credit line matters: a reader has to be able to tell whose claim a date
	// is, because magus did not author any of it.
	assert.Equal(t, "endoflife.date", reg.Sources["eol"].Name)

	// Every input is recorded, so the file states what it was made from.
	require.Len(t, reg.Inputs, 2)
	assert.Equal(t, "go", reg.Inputs[0].Slug, "inputs are sorted, so the bytes are stable")
	assert.Equal(t, "nodejs", reg.Inputs[1].Slug)
	assert.Len(t, reg.Inputs[0].SHA256, 64)
}

// TestGeneratedAtIsDerivedNotStamped is what makes the build reproducible. Reading
// the clock here would mean a rebuild of the same snapshot could never match the
// published file, and then -verify would prove nothing at all.
func TestGeneratedAtIsDerivedNotStamped(t *testing.T) {
	first, err := buildRegistry(upstreamFixtures, "", fixedExpiry)
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)
	second, err := buildRegistry(upstreamFixtures, "", fixedExpiry)
	require.NoError(t, err)

	assert.Equal(t, first.GeneratedAt, second.GeneratedAt)
	assert.False(t, first.GeneratedAt.IsZero(), "generated_at must come from the newest input")
	assert.True(t, first.GeneratedAt.Before(time.Now()), "and it is a real timestamp, not now")
}

// TestBuildIsByteStable is the property -verify rests on: same inputs, identical
// bytes. It also guards the encoder hazard that bit the release index, where
// omitempty on a bool differed under GOEXPERIMENT=jsonv2.
func TestBuildIsByteStable(t *testing.T) {
	var previous string
	for range 3 {
		reg, err := buildRegistry(upstreamFixtures, "", fixedExpiry)
		require.NoError(t, err)
		data, err := json.Marshal(reg)
		require.NoError(t, err)
		if previous != "" {
			require.Equal(t, previous, string(data), "the transform is not deterministic")
		}
		previous = string(data)
	}
}

// TestVerifyDetectsAPublishedFileThatDoesNotFollow is the third-party check: a
// subverted pipeline shows up as a diff someone else found, not as trust nobody
// tested.
func TestVerifyDetectsAPublishedFileThatDoesNotFollow(t *testing.T) {
	reg, err := buildRegistry(upstreamFixtures, "", fixedExpiry)
	require.NoError(t, err)
	data, err := json.Marshal(reg)
	require.NoError(t, err)
	data = append(data, '\n')

	dir := t.TempDir()
	honest := filepath.Join(dir, "index.json")
	require.NoError(t, os.WriteFile(honest, data, 0o644))
	require.NoError(t, verifyRegistry(honest, data))

	tampered := filepath.Join(dir, "tampered.json")
	require.NoError(t, os.WriteFile(tampered, append([]byte(" "), data...), 0o644))
	require.ErrorContains(t, verifyRegistry(tampered, data), "does NOT reproduce")
}

// TestRegistryBuildSignsWithItsOwnKey: MAGUS_SIGNING_KEY must not reach this path.
// It is quarantined to one job on a v* tag, and a nightly job whose input is
// hundreds of third-party responses is precisely where it must not appear.
func TestRegistryBuildSignsWithItsOwnKey(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	t.Setenv("MAGUS_REGISTRY_KEY", hex.EncodeToString(priv))
	t.Setenv("MAGUS_SIGNING_KEY", "")

	out := t.TempDir()
	require.NoError(t, runRegistryBuild([]string{
		"-upstream", upstreamFixtures, "-out", out, "-expires", fixedExpiry,
	}))

	data, err := os.ReadFile(filepath.Join(out, "index.json"))
	require.NoError(t, err)
	sig, err := os.ReadFile(filepath.Join(out, "index.json.sig"))
	require.NoError(t, err)
	assert.True(t, ed25519.Verify(pub, data, sig), "the registry key signed the bytes it wrote")

	// And the client accepts what the producer emits - the two schemas live in
	// different packages, so nothing but this stops them drifting apart.
	var got registry.Registry
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, registry.SchemaVersion, got.SchemaVersion)
	assert.Contains(t, got.EOL, "nodejs")
	assert.False(t, got.GeneratedAt.IsZero())
}

// TestRegistryBuildUnsignedIsFatalUnlessAsked mirrors release-index: an unsigned
// artifact must never pass silently, because that is exactly how index.json.sig
// came to 404 for every user.
func TestRegistryBuildUnsignedIsFatalUnlessAsked(t *testing.T) {
	t.Setenv("MAGUS_REGISTRY_KEY", "")
	out := t.TempDir()

	err := runRegistryBuild([]string{"-upstream", upstreamFixtures, "-out", out, "-expires", fixedExpiry})
	require.ErrorContains(t, err, "-no-sign")

	require.NoError(t, runRegistryBuild([]string{
		"-upstream", upstreamFixtures, "-out", out, "-expires", fixedExpiry, "-no-sign",
	}))
	assert.FileExists(t, filepath.Join(out, "index.json"))
	_, statErr := os.Stat(filepath.Join(out, "index.json.sig"))
	assert.True(t, os.IsNotExist(statErr), "no signature under -no-sign")
}

// TestBuildRegistryCarriesTheReleaseList: one file, two categories, so a new binary
// needs one fetch rather than two half-synced ones.
func TestBuildRegistryCarriesTheReleaseList(t *testing.T) {
	reg, err := buildRegistry(upstreamFixtures, filepath.Join("..", "..", "releases"), fixedExpiry)
	require.NoError(t, err)
	require.NotEmpty(t, reg.Releases)
	assert.Equal(t, "v0.3.0", reg.Releases[0].Version, "newest first, as loadManifests returns them")
	assert.NotEmpty(t, reg.Releases[0].Artifacts[0].SHA256, "artifacts stay pinned")
}
