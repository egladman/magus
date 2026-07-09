package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestMigrateRoundtrip verifies that the migration of CHANGELOG.md produces
// manifests whose body fields are pre-trimmed (no leading/trailing whitespace).
// This is required so scribe.buzz can use them verbatim as Atom feed summaries.
func TestMigrateRoundtrip(t *testing.T) {
	changelogPath := filepath.Join("..", "..", "CHANGELOG.md")
	if _, err := os.Stat(changelogPath); err != nil {
		t.Skip("CHANGELOG.md not found; skipping")
	}

	dir := t.TempDir()
	if err := runMigrate([]string{"-changelog", changelogPath, "-out", dir}); err != nil {
		t.Fatalf("runMigrate: %v", err)
	}

	entries, err := loadManifests(dir)
	if err != nil {
		t.Fatalf("loadManifests: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no manifests written")
	}

	for _, m := range entries {
		if m.Body == "" {
			t.Errorf("%s: body is empty", m.Version)
		}
		if strings.HasPrefix(m.Body, "\n") {
			t.Errorf("%s: body has leading newline (must be pre-trimmed)", m.Version)
		}
		if strings.HasSuffix(m.Body, "\n") {
			t.Errorf("%s: body has trailing newline (must be pre-trimmed)", m.Version)
		}
		if m.Version == "" {
			t.Error("manifest has empty version")
		}
		if m.Date == "" {
			t.Error("manifest has empty date")
		}
		if len(m.Artifacts) == 0 {
			t.Errorf("%s: no artifacts", m.Version)
		}
	}
}

// TestReleaseIndexSignAndVerify generates an ephemeral Ed25519 key, constructs
// an index.json, signs it, and verifies the signature. Proves the sign/verify
// loop works without the production MAGUS_SIGNING_KEY.
func TestReleaseIndexSignAndVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

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
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	data = append(data, '\n')

	sig := ed25519.Sign(priv, data)

	if !verifyIndexSig(data, sig, pub) {
		t.Fatal("signature verification failed")
	}

	// Tampering must fail.
	tampered := append([]byte(nil), data...)
	tampered[0] ^= 0xFF
	if verifyIndexSig(tampered, sig, pub) {
		t.Fatal("verification should fail on tampered data")
	}
}

// TestReleaseIndexJSON verifies the index.json structure carries schema_version.
func TestReleaseIndexJSON(t *testing.T) {
	manifests := []ReleaseManifest{
		{Version: "v0.2.0", Date: "2026-08-01", Body: "### Added\n\n- new feature"},
		{Version: "v0.1.0", Date: "2026-07-05", Body: "### Added\n\n- initial"},
	}
	idx := ReleaseIndex{SchemaVersion: 1, Releases: manifests}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sv, ok := parsed["schema_version"].(float64); !ok || int(sv) != 1 {
		t.Errorf("schema_version: got %v, want 1", parsed["schema_version"])
	}
	releases, ok := parsed["releases"].([]any)
	if !ok {
		t.Fatalf("releases: expected array, got %T", parsed["releases"])
	}
	if len(releases) != 2 {
		t.Fatalf("expected 2 releases, got %d", len(releases))
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
	}
	for _, c := range cases {
		got := compareSemver(c.a, c.b)
		if got > 0 {
			got = 1
		} else if got < 0 {
			got = -1
		}
		if got != c.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestLoadManifestsSortedNewestFirst verifies that loadManifests returns entries newest-first.
func TestLoadManifestsSortedNewestFirst(t *testing.T) {
	dir := t.TempDir()
	for _, version := range []string{"v0.1.0", "v0.3.0", "v0.2.0"} {
		m := ReleaseManifest{Version: version, Date: "2026-07-01", Body: "### Added\n\n- x"}
		writeManifestFile(t, dir, m)
	}

	manifests, err := loadManifests(dir)
	if err != nil {
		t.Fatalf("loadManifests: %v", err)
	}
	if len(manifests) != 3 {
		t.Fatalf("expected 3 manifests, got %d", len(manifests))
	}
	want := []string{"v0.3.0", "v0.2.0", "v0.1.0"}
	for i, w := range want {
		if manifests[i].Version != w {
			t.Errorf("manifests[%d].Version = %q, want %q", i, manifests[i].Version, w)
		}
	}
}

// TestRunReleaseIndex_NoSignKey verifies runReleaseIndex writes index.json
// without MAGUS_SIGNING_KEY set (signing is skipped, no .sig written).
func TestRunReleaseIndex_NoSignKey(t *testing.T) {
	t.Setenv("MAGUS_SIGNING_KEY", "")

	dir := t.TempDir()
	writeManifestFile(t, dir, ReleaseManifest{
		Version: "v0.1.0",
		Date:    "2026-07-05",
		Body:    "### Added\n\n- x",
	})

	outDir := t.TempDir()
	if err := runReleaseIndex([]string{"-releases", dir, "-out", outDir}); err != nil {
		t.Fatalf("runReleaseIndex: %v", err)
	}

	idxPath := filepath.Join(outDir, "index.json")
	data, err := os.ReadFile(idxPath)
	if err != nil {
		t.Fatalf("read index.json: %v", err)
	}

	var idx ReleaseIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatalf("unmarshal index.json: %v", err)
	}
	if idx.SchemaVersion != 1 {
		t.Errorf("schema_version: got %d, want 1", idx.SchemaVersion)
	}
	if len(idx.Releases) != 1 {
		t.Errorf("expected 1 release, got %d", len(idx.Releases))
	}

	// No .sig file should exist.
	if _, err := os.Stat(idxPath + ".sig"); !os.IsNotExist(err) {
		t.Error("expected no .sig file when MAGUS_SIGNING_KEY is unset")
	}
}

// TestRunReleaseIndex_WithEphemeralKey tests signing with a freshly generated
// Ed25519 key, then verifies the .sig against the matching public key.
func TestRunReleaseIndex_WithEphemeralKey(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	t.Setenv("MAGUS_SIGNING_KEY", hex.EncodeToString(priv))

	dir := t.TempDir()
	writeManifestFile(t, dir, ReleaseManifest{
		Version: "v0.1.0",
		Date:    "2026-07-05",
		Body:    "### Added\n\n- x",
	})

	outDir := t.TempDir()
	if err := runReleaseIndex([]string{"-releases", dir, "-out", outDir}); err != nil {
		t.Fatalf("runReleaseIndex: %v", err)
	}

	idxData, err := os.ReadFile(filepath.Join(outDir, "index.json"))
	if err != nil {
		t.Fatalf("read index.json: %v", err)
	}
	sigData, err := os.ReadFile(filepath.Join(outDir, "index.json.sig"))
	if err != nil {
		t.Fatalf("read index.json.sig: %v", err)
	}

	if !verifyIndexSig(idxData, sigData, pub) {
		t.Fatal("signature verification failed")
	}
}

// writeManifestFile writes a ReleaseManifest to dir/<version>.yaml for tests.
func writeManifestFile(t *testing.T, dir string, m ReleaseManifest) {
	t.Helper()
	data, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("marshal %s: %v", m.Version, err)
	}
	if err := os.WriteFile(filepath.Join(dir, m.Version+".yaml"), data, 0o644); err != nil {
		t.Fatalf("write %s.yaml: %v", m.Version, err)
	}
}
