package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/selfupdate"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestMigrateRoundtrip verifies that the migration of CHANGELOG.md produces
// manifests whose body fields are pre-trimmed (no leading/trailing whitespace).
// This is required so render.buzz can use them verbatim as Atom feed summaries.
func TestMigrateRoundtrip(t *testing.T) {
	changelogPath := filepath.Join("..", "..", "CHANGELOG.md")
	if _, err := os.Stat(changelogPath); err != nil {
		t.Skip("CHANGELOG.md not found; skipping")
	}

	dir := t.TempDir()
	require.NoError(t, runMigrate([]string{"-changelog", changelogPath, "-out", dir}))

	entries, err := loadManifests(dir)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "at least one manifest must be written")

	for _, m := range entries {
		require.Equal(t, ReleaseManifest{
			Version:   m.Version,
			Date:      m.Date,
			Notes:     m.Notes,
			Body:      m.Body,
			Artifacts: m.Artifacts,
			Yanked:    m.Yanked,
		}, m, "manifest %s round-trips identically (whole-struct check)", m.Version)
		require.NotEmpty(t, m.Body, "%s: body is empty", m.Version)
		require.NotEmpty(t, m.Version, "manifest has empty version")
		require.NotEmpty(t, m.Date, "manifest has empty date")
		require.NotEmpty(t, m.Artifacts, "%s: no artifacts", m.Version)
		// Body must be trimmed: no leading or trailing newline.
		require.False(t, len(m.Body) > 0 && m.Body[0] == '\n', "%s: body has leading newline", m.Version)
		require.False(t, len(m.Body) > 0 && m.Body[len(m.Body)-1] == '\n', "%s: body has trailing newline", m.Version)
	}
}

// TestReleaseIndexSignAndVerify generates an ephemeral Ed25519 key, constructs
// an index.json, signs it, and verifies the signature. Proves the sign/verify
// loop works without the production MAGUS_SIGNING_KEY.
func TestReleaseIndexSignAndVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	manifests := []ReleaseManifest{
		{
			Version: "v0.1.0",
			Date:    "2026-07-05",
			Body:    "### Added\n\n- test item",
			Artifacts: []ReleaseArtifact{
				{Name: "magus_v0.1.0_linux_amd64.tar.gz", Platform: "linux/amd64", Size: "1234", SHA256: "abcdef"},
			},
		},
	}
	data, err := json.Marshal(buildIndex(manifests, "testkeyid", "2099-01-01T00:00:00Z", nil))
	require.NoError(t, err)
	data = append(data, '\n')

	sig := ed25519.Sign(priv, data)

	require.True(t, verifyIndexSig(data, sig, pub), "signature must verify")

	// Tampering must fail.
	tampered := append([]byte(nil), data...)
	tampered[0] ^= 0xFF
	require.False(t, verifyIndexSig(tampered, sig, pub), "verification must fail on tampered data")
}

// TestBuildIndexEmitsOnlyTheServedSchema pins the exact bytes of the file
// `magus self update` downloads, and pins them BYTEWISE rather than semantically.
// A signature covers bytes, so an encoder difference is a real defect here even
// when the JSON means the same thing - which is how omitempty on Yanked was caught
// emitting `"yanked":false` under GOEXPERIMENT=jsonv2 and nothing without it.
//
// The manifest's prose must not reach the file either: every client fetches it on
// every check, and date/notes/body would multiply its size for fields nothing reads.
func TestBuildIndexEmitsOnlyTheServedSchema(t *testing.T) {
	manifests := []ReleaseManifest{
		{
			Version: "v0.2.0",
			Date:    "2026-08-01",
			Notes:   ReleaseNotes{Added: []string{"new feature"}},
			Body:    "### Added\n\n- new feature",
			Artifacts: []ReleaseArtifact{
				{Name: "magus_v0.2.0_linux_amd64_static.tar.gz", Platform: "linux/amd64", Size: "12", SHA256: "ab"},
			},
		},
		{Version: "v0.1.0", Date: "2026-07-05", Body: "### Added\n\n- initial", Yanked: true},
	}
	data, err := json.Marshal(buildIndex(manifests, "testkeyid", "2099-01-01T00:00:00Z", []string{"deadbeefdeadbeef"}))
	require.NoError(t, err)
	require.Equal(t, `{"schema_version":1,"key_id":"testkeyid","revoked":["deadbeefdeadbeef"],"expires_at":"2099-01-01T00:00:00Z","releases":[`+
		`{"version":"v0.2.0","artifacts":[{"name":"magus_v0.2.0_linux_amd64_static.tar.gz","platform":"linux/amd64","size":"12","sha256":"ab"}]},`+
		`{"version":"v0.1.0","yanked":true,"artifacts":[]}`+
		`]}`, string(data))
}

// buildIndexForTest fills in the signer and lifetime a caller does not care about.
func buildIndexForTest(manifests []ReleaseManifest) ReleaseIndex {
	return buildIndex(manifests, "testkeyid", "2099-01-01T00:00:00Z", nil)
}

// TestBuildIndexParsesAsTheClientReadsIt runs the emitted bytes through the
// reader in internal/selfupdate. The two schemas are declared in different
// packages, so nothing but this stops them drifting apart.
func TestBuildIndexParsesAsTheClientReadsIt(t *testing.T) {
	data, err := json.Marshal(buildIndexForTest([]ReleaseManifest{
		{Version: "v0.2.0", Artifacts: []ReleaseArtifact{{Name: "magus_v0.2.0_linux_amd64_static.tar.gz"}}},
		{Version: "v0.3.0", Yanked: true},
	}))
	require.NoError(t, err)

	var idx selfupdate.ReleaseIndex
	require.NoError(t, json.Unmarshal(data, &idx))
	require.Equal(t, 1, idx.SchemaVersion)

	rel, err := selfupdate.SelectRelease(&idx, "")
	require.NoError(t, err)
	require.Equal(t, "v0.2.0", rel.Version, "the yanked v0.3.0 must not be selected")
	require.Equal(t, "magus_v0.2.0_linux_amd64_static.tar.gz", rel.Artifacts[0].Name)
}

// servedIndexDir is the tracked docs/gen/public/release, the one directory under
// docs/gen/ that a render never writes.
const servedIndexDir = "../../docs/gen/public/release"

// mustLoadShippedManifests reads this repository's own releases/, newest-first.
func mustLoadShippedManifests(t *testing.T) []ReleaseManifest {
	t.Helper()
	manifests, err := loadManifests(filepath.Join("..", "..", "releases"))
	require.NoError(t, err)
	require.NotEmpty(t, manifests, "releases/ must hold a manifest per shipped release")
	return manifests
}

// TestServedIndexMatchesTheManifests catches a manifest edited without re-running
// release-index. The usual regenerate-and-diff drift gate is the wrong shape here:
// index.json.sig covers these exact bytes, so a gate allowed to REWRITE the file
// would silently invalidate the signature it is meant to protect. Comparing can
// only ever report.
//
// The rebuild reuses the served file's own key_id and expires_at. Those are not
// functions of the manifests - one is who signed, the other is a clock reading - so
// asserting them here would only assert that time had not passed.
func TestServedIndexMatchesTheManifests(t *testing.T) {
	got, err := os.ReadFile(filepath.Join(servedIndexDir, "index.json"))
	require.NoError(t, err)
	var served ReleaseIndex
	require.NoError(t, json.Unmarshal(got, &served))

	want, err := json.Marshal(buildIndex(mustLoadShippedManifests(t), served.KeyID, served.ExpiresAt, served.Revoked))
	require.NoError(t, err)
	require.Equal(t, string(want)+"\n", string(got),
		"docs/gen/public/release/index.json is stale: re-run `magus-utils release-index` and re-sign it")
}

// TestServedIndexIsSignedByTheRing proves the published pair is one a shipped binary
// can verify - the failure that made `magus self update` exit 1 for every user of
// v0.3.0 - and that it names the key that actually signed it. Only a tag build (or the
// release-index workflow) holds the key, so the signature arrives after the index; the
// skip is that window and nothing else.
func TestServedIndexIsSignedByTheRing(t *testing.T) {
	sig, err := os.ReadFile(filepath.Join(servedIndexDir, "index.json.sig"))
	if os.IsNotExist(err) {
		t.Skip("index.json.sig not committed yet; run the release-index workflow")
	}
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(servedIndexDir, "index.json"))
	require.NoError(t, err)

	signer, err := selfupdate.ReleaseKeys.Verify(data, sig)
	require.NoError(t, err)
	var served ReleaseIndex
	require.NoError(t, json.Unmarshal(data, &served))
	require.Equal(t, signer.ID, served.KeyID, "the index must name the key that signed it")
}

// TestServedIndexHasNotExpired is the warning the doctor check gives interactively,
// as a gate: an index past expires_at is refused by every client, so it must never be
// the thing sitting in the repository.
func TestServedIndexHasNotExpired(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(servedIndexDir, "index.json"))
	require.NoError(t, err)
	var served ReleaseIndex
	require.NoError(t, json.Unmarshal(data, &served))
	require.NotEmpty(t, served.ExpiresAt, "an index with no expires_at can be replayed forever")

	deadline, err := time.Parse(time.RFC3339, served.ExpiresAt)
	require.NoError(t, err)
	require.True(t, time.Now().Before(deadline),
		"the served index expired at %s: run the Release index workflow", served.ExpiresAt)
}

// TestShippedManifestsPinEveryArtifact guards the defect that made the served
// index useless: every size and sha256 in releases/*.yaml was the empty string,
// so a signature would have covered a file that pinned nothing.
func TestShippedManifestsPinEveryArtifact(t *testing.T) {
	for _, m := range mustLoadShippedManifests(t) {
		require.NotEmpty(t, m.Artifacts, "%s: no artifacts", m.Version)
		for _, a := range m.Artifacts {
			require.NotEmpty(t, a.Size, "%s: %s has no size", m.Version, a.Name)
			require.NotEmpty(t, a.SHA256, "%s: %s has no sha256", m.Version, a.Name)
		}
	}
}

// TestCompareSemver verifies numeric semver sort direction.
func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.2.0", "v0.1.0", 1},
		{"v0.1.0", "v0.2.0", -1},
		{"v1.0.0", "v0.9.9", 1},
		{"v0.1.0", "v0.1.0", 0},
		{"v0.10.0", "v0.2.0", 1}, // numeric: 10 > 2, not lexicographic
		{"v0.2.0", "v0.10.0", -1},
	}
	for _, c := range cases {
		got := compareSemver(c.a, c.b)
		sign := 0
		if got > 0 {
			sign = 1
		} else if got < 0 {
			sign = -1
		}
		require.Equal(t, c.want, sign, "compareSemver(%q, %q)", c.a, c.b)
	}
}

// TestLoadManifestsSortedNewestFirst verifies that loadManifests returns entries
// newest-first by numeric semver (v0.10.0 before v0.2.0).
func TestLoadManifestsSortedNewestFirst(t *testing.T) {
	dir := t.TempDir()
	// yaml.Unmarshal produces empty slices for empty YAML lists; use []T{} not nil.
	wantVersions := []string{"v0.10.0", "v0.3.0", "v0.2.0", "v0.1.0"}
	seeds := []ReleaseManifest{
		{Version: "v0.10.0", Date: "2026-07-03", Body: "### Added\n\n- ten"},
		{Version: "v0.3.0", Date: "2026-07-02", Body: "### Added\n\n- three"},
		{Version: "v0.2.0", Date: "2026-07-01", Body: "### Added\n\n- two"},
		{Version: "v0.1.0", Date: "2026-06-30", Body: "### Added\n\n- one"},
	}
	// Write in scrambled order to ensure the sort is doing the work.
	for _, m := range []ReleaseManifest{seeds[2], seeds[0], seeds[3], seeds[1]} {
		writeManifestFile(t, dir, m)
	}

	got, err := loadManifests(dir)
	require.NoError(t, err)
	require.Len(t, got, len(wantVersions), "all manifests loaded")
	for i, wantVer := range wantVersions {
		require.Equal(t, wantVer, got[i].Version, "position %d must be %s", i, wantVer)
	}
}

// seedReleaseIndexInput writes one manifest and returns (releasesDir, outDir).
func seedReleaseIndexInput(t *testing.T) (string, string) {
	t.Helper()
	relDir := t.TempDir()
	writeManifestFile(t, relDir, ReleaseManifest{
		Version:   "v0.1.0",
		Date:      "2026-07-05",
		Body:      "### Added\n\n- x",
		Artifacts: []ReleaseArtifact{{Name: "magus_v0.1.0_linux_amd64_static.tar.gz", Platform: "linux/amd64", Size: "9", SHA256: "ab"}},
	})
	return relDir, filepath.Join(t.TempDir(), "release")
}

// TestRunReleaseIndex_UnsetKeyIsFatal covers the defect directly: a release job
// whose signing key was missing used to write index.json, warn, and exit 0,
// publishing an index whose .sig 404s for every client.
func TestRunReleaseIndex_UnsetKeyIsFatal(t *testing.T) {
	t.Setenv("MAGUS_SIGNING_KEY", "")
	relDir, outDir := seedReleaseIndexInput(t)

	err := runReleaseIndex([]string{"-releases", relDir, "-out", outDir})
	require.Error(t, err, "an unsigned index must not pass silently")
	require.Contains(t, err.Error(), "-no-sign", "the error must name the opt-out")
}

// TestRunReleaseIndex_NoSign writes the index without a signature when asked.
func TestRunReleaseIndex_NoSign(t *testing.T) {
	t.Setenv("MAGUS_SIGNING_KEY", "")
	relDir, outDir := seedReleaseIndexInput(t)

	require.NoError(t, runReleaseIndex([]string{"-releases", relDir, "-out", outDir, "-no-sign"}))

	_, err := os.Stat(filepath.Join(outDir, "index.json"))
	require.NoError(t, err, "index.json is written even unsigned")
	_, err = os.Stat(filepath.Join(outDir, "index.json.sig"))
	require.True(t, os.IsNotExist(err), "no .sig expected under -no-sign")
}

// TestRunReleaseIndex_WithEphemeralKey checks the sig covers the exact bytes
// written, which is the contract the client verifies before parsing them.
func TestRunReleaseIndex_WithEphemeralKey(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	t.Setenv("MAGUS_SIGNING_KEY", hex.EncodeToString(priv))
	overrideVerifyPubKey = pub
	t.Cleanup(func() { overrideVerifyPubKey = nil })

	relDir, outDir := seedReleaseIndexInput(t)
	require.NoError(t, runReleaseIndex([]string{"-releases", relDir, "-out", outDir}))

	idxData, err := os.ReadFile(filepath.Join(outDir, "index.json"))
	require.NoError(t, err)
	sigData, err := os.ReadFile(filepath.Join(outDir, "index.json.sig"))
	require.NoError(t, err)
	require.True(t, verifyIndexSig(idxData, sigData, pub), "sig must verify against the written bytes")
	require.NoError(t, verifyIndexSigFile(outDir, pub))

	mutated := append([]byte(nil), idxData...)
	mutated[0] ^= 0x01
	require.False(t, verifyIndexSig(mutated, sigData, pub), "sig must not verify after mutation")
}

// TestRunReleaseIndex_SigningKeyMustMatchTheEmbeddedKey is the check that catches
// a rotation gone wrong: a signature nothing in the field can verify.
func TestRunReleaseIndex_SigningKeyMustMatchTheEmbeddedKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	t.Setenv("MAGUS_SIGNING_KEY", hex.EncodeToString(priv))

	relDir, outDir := seedReleaseIndexInput(t)
	err = runReleaseIndex([]string{"-releases", relDir, "-out", outDir})
	require.Error(t, err, "signing with a key no binary carries must fail the release")
	require.Contains(t, err.Error(), "signature verification failed")
}

// TestRunReleaseIndex_NoManifests verifies an empty releases/ is refused rather
// than published: SelectRelease rejects an index with no releases, so an empty
// one strands every client that fetches it.
func TestRunReleaseIndex_NoManifests(t *testing.T) {
	err := runReleaseIndex([]string{"-releases", t.TempDir(), "-out", t.TempDir()})
	require.Error(t, err, "must fail when there is nothing to index")
}

// TestFileSizeAndSHA256 verifies size and digest against a known temp file.
func TestFileSizeAndSHA256(t *testing.T) {
	content := []byte("hello world\n")
	f, err := os.CreateTemp(t.TempDir(), "test-*.txt")
	require.NoError(t, err)
	_, err = f.Write(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	h := sha256.Sum256(content)
	wantDigest := hex.EncodeToString(h[:])
	wantSize := int64(len(content))

	gotSize, gotDigest, err := fileSizeAndSHA256(f.Name())
	require.NoError(t, err)
	require.Equal(t, wantSize, gotSize, "file size")
	require.Equal(t, wantDigest, gotDigest, "sha256 digest")
}

// TestPlatformFromName verifies tarball filename -> platform string mapping.
func TestPlatformFromName(t *testing.T) {
	ver := "v0.2.0"
	cases := []struct {
		name     string
		wantPlat string
	}{
		{"magus_v0.2.0_linux_amd64.tar.gz", "linux/amd64"},
		{"magus_v0.2.0_linux_arm64.tar.gz", "linux/arm64"},
		{"magus_v0.2.0_linux_armv6.tar.gz", "linux/armv6"},
		{"magus_v0.2.0_linux_armv7.tar.gz", "linux/armv7"},
		{"magus_v0.2.0_darwin_amd64.tar.gz", "darwin/amd64"},
		{"magus_v0.2.0_darwin_arm64.tar.gz", "darwin/arm64"},
		{"magus_v0.2.0_windows_amd64.tar.gz", "windows/amd64"},
		{"magus_v0.2.0_windows_arm64.tar.gz", "windows/arm64"},
		// Both variants of one platform resolve to that platform: the variant token is
		// not part of the platform, and leaving it on yielded "linux/amd64_static".
		{"magus_v0.2.0_linux_amd64_static.tar.gz", "linux/amd64"},
		{"magus_v0.2.0_darwin_arm64_static.tar.gz", "darwin/arm64"},
		{"SHA256SUMS", ""},
		{"SHA256SUMS.sig", ""},
		{"magus-release.pem", ""},
		{"unrelated.txt", ""},
	}
	for _, c := range cases {
		require.Equal(t, c.wantPlat, platformFromName(c.name, ver), "platformFromName(%q, %q)", c.name, ver)
	}
}

// TestIsReleaseAsset verifies which filenames are considered release assets.
func TestIsReleaseAsset(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"magus_v0.2.0_linux_amd64.tar.gz", true},
		{"magus_v0.2.0_darwin_arm64.tar.gz", true},
		{"SHA256SUMS", true},
		{"SHA256SUMS.sig", true},
		{"magus-release.pem", false},
		{"README.md", false},
		{"index.json", false},
		{".hidden", false},
	}
	for _, c := range cases {
		require.Equal(t, c.want, isReleaseAsset(c.name), "isReleaseAsset(%q)", c.name)
	}
}

// TestRunCut_HappyPath verifies that runCut writes a complete ReleaseManifest
// from a directory containing real artifacts and a CHANGELOG.md with an
// [Unreleased] section.
func TestRunCut_HappyPath(t *testing.T) {
	// Build a temp artifacts directory with a tarball and SHA256SUMS.
	artifactsDir := t.TempDir()
	tarName := "magus_v0.2.0_linux_amd64.tar.gz"
	tarContent := []byte("fake tarball content for test")
	require.NoError(t, os.WriteFile(filepath.Join(artifactsDir, tarName), tarContent, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(artifactsDir, "SHA256SUMS"), []byte("abc123  "+tarName+"\n"), 0o644))

	// Build a temp CHANGELOG.md with an Unreleased section.
	changelogContent := "# Changelog\n\n## [Unreleased]\n\n### Added\n\n- brand new feature\n\n## [v0.1.0] - 2026-07-05\n\nOld.\n"
	changelogPath := filepath.Join(t.TempDir(), "CHANGELOG.md")
	require.NoError(t, os.WriteFile(changelogPath, []byte(changelogContent), 0o644))

	outDir := t.TempDir()
	require.NoError(t, runCut([]string{
		"-version", "v0.2.0",
		"-artifacts", artifactsDir,
		"-changelog", changelogPath,
		"-out", outDir,
	}))

	// Read and unmarshal the written manifest.
	data, err := os.ReadFile(filepath.Join(outDir, "v0.2.0.yaml"))
	require.NoError(t, err)
	var got ReleaseManifest
	require.NoError(t, yaml.Unmarshal(data, &got))

	// Compute expected artifact digests.
	tarH := sha256.Sum256(tarContent)
	tarDigest := hex.EncodeToString(tarH[:])
	ckContent := []byte("abc123  " + tarName + "\n")
	ckH := sha256.Sum256(ckContent)
	ckDigest := hex.EncodeToString(ckH[:])

	// os.ReadDir returns entries in alphabetical order: "SHA256SUMS" (uppercase S=0x53)
	// sorts before "magus_..." (lowercase m=0x6d) in ASCII, so SHA256SUMS appears first.
	want := ReleaseManifest{
		Version: "v0.2.0",
		Date:    got.Date, // date is time.Now()-derived; just check it is populated
		Notes: ReleaseNotes{
			Added: []string{"brand new feature"},
		},
		Body: "### Added\n\n- brand new feature",
		Artifacts: []ReleaseArtifact{
			{
				Name:     "SHA256SUMS",
				Platform: "",
				Size:     fmt.Sprintf("%d", len(ckContent)),
				SHA256:   ckDigest,
			},
			{
				Name:     tarName,
				Platform: "linux/amd64",
				Size:     fmt.Sprintf("%d", len(tarContent)),
				SHA256:   tarDigest,
			},
		},
	}
	require.NotEmpty(t, got.Date, "date must be populated")
	require.Equal(t, want, got, "ReleaseManifest matches expected whole struct")
}

// TestCutThenGenerateChangelogDoesNotDuplicate walks the pair of commands a
// release runs. CHANGELOG.md is generated back out of the manifests, so a cut
// that left [Unreleased] populated would print the shipped entries twice.
func TestCutThenGenerateChangelogDoesNotDuplicate(t *testing.T) {
	artifactsDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(artifactsDir, "magus_v0.2.0_linux_amd64_static.tar.gz"), []byte("x"), 0o644))

	changelogPath := filepath.Join(t.TempDir(), "CHANGELOG.md")
	require.NoError(t, os.WriteFile(changelogPath,
		[]byte("# Changelog\n\n## [Unreleased]\n\n### Added\n\n- brand new feature\n\n## [v0.1.0] - 2026-07-05\n\nOld.\n"), 0o644))

	relDir := t.TempDir()
	writeManifestFile(t, relDir, ReleaseManifest{Version: "v0.1.0", Date: "2026-07-05", Body: "Old."})
	require.NoError(t, runCut([]string{
		"-version", "v0.2.0", "-artifacts", artifactsDir, "-changelog", changelogPath, "-out", relDir,
	}))
	require.NoError(t, runGenerateChangelog([]string{"-releases", relDir, "-changelog", changelogPath}))

	got, err := os.ReadFile(changelogPath)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(got), "- brand new feature"), "the entry belongs to v0.2.0 alone:\n%s", got)
	require.Contains(t, string(got), "## [Unreleased]\n\n## [v0.2.0]", "Unreleased is emptied, not removed")
}

// TestRunCut_ImmutabilityGuard verifies that runCut refuses to overwrite an
// existing manifest (release manifests are immutable once committed).
func TestRunCut_ImmutabilityGuard(t *testing.T) {
	artifactsDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(artifactsDir, "magus_v0.1.0_linux_amd64.tar.gz"), []byte("x"), 0o644))

	changelogPath := filepath.Join(t.TempDir(), "CHANGELOG.md")
	require.NoError(t, os.WriteFile(changelogPath, []byte("# Changelog\n\n## [Unreleased]\n\n### Added\n\n- x\n"), 0o644))

	outDir := t.TempDir()
	// Pre-create the output file to trigger the immutability check.
	require.NoError(t, os.WriteFile(filepath.Join(outDir, "v0.1.0.yaml"), []byte("existing"), 0o644))

	err := runCut([]string{
		"-version", "v0.1.0",
		"-artifacts", artifactsDir,
		"-changelog", changelogPath,
		"-out", outDir,
	})
	require.Error(t, err, "must refuse to overwrite an existing manifest")
	require.Contains(t, err.Error(), "already exists", "error must mention existing file")
}

// TestRunCut_NoArtifactsGuard verifies that runCut refuses to write a hollow
// manifest when the artifacts directory contains no recognized release assets.
func TestRunCut_NoArtifactsGuard(t *testing.T) {
	artifactsDir := t.TempDir()
	// Only an unrelated file - no tarballs or SHA256SUMS.
	require.NoError(t, os.WriteFile(filepath.Join(artifactsDir, "README.txt"), []byte("ignore me"), 0o644))

	changelogPath := filepath.Join(t.TempDir(), "CHANGELOG.md")
	require.NoError(t, os.WriteFile(changelogPath, []byte("# Changelog\n\n## [Unreleased]\n\n### Added\n\n- x\n"), 0o644))

	err := runCut([]string{
		"-version", "v0.2.0",
		"-artifacts", artifactsDir,
		"-changelog", changelogPath,
		"-out", t.TempDir(),
	})
	require.Error(t, err, "must refuse when no release artifacts are found")
	require.Contains(t, err.Error(), "no release artifacts found", "error must name the problem")
}

// TestRunGenerateChangelog verifies that runGenerateChangelog rewrites CHANGELOG.md
// from releases/*.yaml, preserving the [Unreleased] section verbatim and
// regenerating released sections from manifests.
func TestRunGenerateChangelog(t *testing.T) {
	relDir := t.TempDir()
	writeManifestFile(t, relDir, ReleaseManifest{
		Version: "v0.2.0",
		Date:    "2026-08-01",
		Body:    "### Added\n\n- new thing",
	})
	writeManifestFile(t, relDir, ReleaseManifest{
		Version: "v0.1.0",
		Date:    "2026-07-05",
		Body:    "### Added\n\n- first",
	})

	// CHANGELOG.md with an existing [Unreleased] section to preserve.
	changelogPath := filepath.Join(t.TempDir(), "CHANGELOG.md")
	initial := "# Changelog\n\n## [Unreleased]\n\n### Added\n\n- coming soon\n\n## [v0.1.0] - 2026-07-05\n\nOld content to be replaced.\n"
	require.NoError(t, os.WriteFile(changelogPath, []byte(initial), 0o644))

	require.NoError(t, runGenerateChangelog([]string{
		"-releases", relDir,
		"-changelog", changelogPath,
	}))

	data, err := os.ReadFile(changelogPath)
	require.NoError(t, err)
	body := string(data)

	require.Contains(t, body, "## [Unreleased]", "Unreleased heading preserved")
	require.Contains(t, body, "- coming soon", "Unreleased content preserved")
	require.Contains(t, body, "## [v0.2.0] - 2026-08-01", "v0.2.0 heading generated")
	require.Contains(t, body, "### Added\n\n- new thing", "v0.2.0 body generated")
	require.Contains(t, body, "## [v0.1.0] - 2026-07-05", "v0.1.0 heading generated")
	require.Contains(t, body, "### Added\n\n- first", "v0.1.0 body generated")
	// v0.2.0 must appear before v0.1.0 (newest-first).
	require.Less(t, index(body, "## [v0.2.0]"), index(body, "## [v0.1.0]"), "newest release first")
}

// TestReadUnreleasedSection verifies extraction of the [Unreleased] body.
func TestReadUnreleasedSection(t *testing.T) {
	changelog := "# Changelog\n\n## [Unreleased]\n\n### Added\n\n- pending\n\n## [v0.1.0] - 2026-07-05\n\nReleased.\n"
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	require.NoError(t, os.WriteFile(path, []byte(changelog), 0o644))

	got, err := readUnreleasedSection(path)
	require.NoError(t, err)
	require.Contains(t, got, "### Added", "section heading preserved")
	require.Contains(t, got, "- pending", "item preserved")
	// Must not include the released section.
	require.NotContains(t, got, "v0.1.0", "released section excluded")

	// Non-existent file returns empty string, no error.
	empty, err := readUnreleasedSection(filepath.Join(t.TempDir(), "none.md"))
	require.NoError(t, err)
	require.Equal(t, "", empty, "missing file returns empty string")
}

// index returns the byte offset of substr in s, or panics if not found (test helper).
func index(s, substr string) int {
	for i := range len(s) {
		if i+len(substr) <= len(s) && s[i:i+len(substr)] == substr {
			return i
		}
	}
	panic("substring not found: " + substr)
}

// writeManifestFile writes a ReleaseManifest to dir/<version>.yaml for tests.
func writeManifestFile(t *testing.T, dir string, m ReleaseManifest) {
	t.Helper()
	data, err := yaml.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, m.Version+".yaml"), data, 0o644))
}

// The asset naming scheme changed at v0.4.0: releases up to v0.3.0 wrote `-static` and
// `-cgo`, later ones write `_static`. platformFromName has to read both, or every asset
// already published gets a platform like "darwin/arm64-static" - which is not a platform,
// and is what the release index would then advertise.
func TestPlatformFromNameReadsBothVariantSpellings(t *testing.T) {
	for _, tc := range []struct{ name, version, want string }{
		{"magus_v0.4.0_linux_amd64_static.tar.gz", "v0.4.0", "linux/amd64"},
		{"magus_v0.4.0_linux_amd64.tar.gz", "v0.4.0", "linux/amd64"},
		{"magus_v0.3.0_darwin_arm64-static.tar.gz", "v0.3.0", "darwin/arm64"},
		{"magus_v0.3.0_darwin_arm64.tar.gz", "v0.3.0", "darwin/arm64"},
		{"magus_v0.2.0_linux_amd64-cgo.tar.gz", "v0.2.0", "linux/amd64"},
		{"SHA256SUMS", "v0.3.0", ""},
	} {
		if got := platformFromName(tc.name, tc.version); got != tc.want {
			t.Errorf("platformFromName(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
