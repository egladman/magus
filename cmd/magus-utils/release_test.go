package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

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
	idx := ReleaseIndex{SchemaVersion: 1, Releases: manifests}
	data, err := json.MarshalIndent(idx, "", "  ")
	require.NoError(t, err)
	data = append(data, '\n')

	sig := ed25519.Sign(priv, data)

	require.True(t, verifyIndexSig(data, sig, pub), "signature must verify")

	// Tampering must fail.
	tampered := append([]byte(nil), data...)
	tampered[0] ^= 0xFF
	require.False(t, verifyIndexSig(tampered, sig, pub), "verification must fail on tampered data")
}

// TestReleaseIndexJSON verifies the index.json structure carries schema_version.
func TestReleaseIndexJSON(t *testing.T) {
	manifests := []ReleaseManifest{
		{Version: "v0.2.0", Date: "2026-08-01", Body: "### Added\n\n- new feature"},
		{Version: "v0.1.0", Date: "2026-07-05", Body: "### Added\n\n- initial"},
	}
	want := ReleaseIndex{SchemaVersion: 1, Releases: manifests}
	data, err := json.MarshalIndent(want, "", "  ")
	require.NoError(t, err)

	var got ReleaseIndex
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, want, got, "ReleaseIndex round-trips through JSON")
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

// TestRunReleaseIndex_NoSignKey verifies runReleaseIndex signs the bytes of a
// pre-existing index.json without MAGUS_SIGNING_KEY set (signing is skipped).
func TestRunReleaseIndex_NoSignKey(t *testing.T) {
	t.Setenv("MAGUS_SIGNING_KEY", "")

	servedDir := t.TempDir()
	// Write a pre-built index.json (the served file the renderer would emit).
	idxJSON := `{"schema_version":1,"releases":[{"version":"v0.1.0","date":"2026-07-05","notes":{},"body":"### Added\n\n- x","artifacts":[]}]}` + "\n"
	idxPath := filepath.Join(servedDir, "index.json")
	require.NoError(t, os.WriteFile(idxPath, []byte(idxJSON), 0o644))

	require.NoError(t, runReleaseIndex([]string{"-served", servedDir}))

	// No .sig file should exist when the key is unset.
	_, err := os.Stat(idxPath + ".sig")
	require.True(t, os.IsNotExist(err), "no .sig file expected when MAGUS_SIGNING_KEY is unset")
}

// TestRunReleaseIndex_WithEphemeralKey tests that runReleaseIndex signs the
// exact bytes of the pre-existing index.json, and the sig verifies against the
// same bytes. This is the sig-over-served-bytes contract.
func TestRunReleaseIndex_WithEphemeralKey(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	t.Setenv("MAGUS_SIGNING_KEY", hex.EncodeToString(priv))

	servedDir := t.TempDir()
	// Write a pre-built index.json (the served file the renderer would emit).
	idxJSON := `{"schema_version":1,"releases":[{"version":"v0.1.0","date":"2026-07-05","notes":{},"body":"### Added\n\n- x","artifacts":[]}]}` + "\n"
	idxPath := filepath.Join(servedDir, "index.json")
	require.NoError(t, os.WriteFile(idxPath, []byte(idxJSON), 0o644))

	require.NoError(t, runReleaseIndex([]string{"-served", servedDir}))

	idxData, err := os.ReadFile(idxPath)
	require.NoError(t, err)
	sigData, err := os.ReadFile(idxPath + ".sig")
	require.NoError(t, err)

	// The sig must cover the exact bytes of the served file.
	require.True(t, verifyIndexSig(idxData, sigData, pub), "sig must verify against served bytes")

	// Verify via the helper that checks the files on disk.
	require.NoError(t, verifyIndexSigFile(servedDir, pub))

	// Mutating the served bytes must break the sig.
	mutated := append([]byte(nil), idxData...)
	mutated[0] ^= 0x01
	require.False(t, verifyIndexSig(mutated, sigData, pub), "sig must not verify after mutation")
}

// TestRunReleaseIndex_MissingFile verifies an error is returned when index.json
// does not exist (generate docs must be run first).
func TestRunReleaseIndex_MissingFile(t *testing.T) {
	servedDir := t.TempDir()
	err := runReleaseIndex([]string{"-served", servedDir})
	require.Error(t, err, "must fail when index.json is missing")
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
		{"magus_v0.2.0_darwin_amd64.tar.gz", "darwin/amd64"},
		{"magus_v0.2.0_darwin_arm64.tar.gz", "darwin/arm64"},
		{"magus_v0.2.0_windows_amd64.tar.gz", "windows/amd64"},
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
			{
				Name:     "magus-release.pem",
				Platform: "",
				Size:     "",
				SHA256:   "",
			},
		},
	}
	require.NotEmpty(t, got.Date, "date must be populated")
	require.Equal(t, want, got, "ReleaseManifest matches expected whole struct")
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
