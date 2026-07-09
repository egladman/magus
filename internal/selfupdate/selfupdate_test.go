//go:build !noselfupdate

package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTestKey generates a fresh ephemeral Ed25519 key pair for tests.
// Do NOT use the production private key; the public half is injectable via Options.PubKey.
func makeTestKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv
}

// buildIndex builds a minimal ReleaseIndex JSON body and signs it with priv.
// Returns the JSON bytes and signature bytes.
func buildIndex(t *testing.T, priv ed25519.PrivateKey, releases []IndexRelease) ([]byte, []byte) {
	t.Helper()
	idx := ReleaseIndex{SchemaVersion: 1, Releases: releases}
	data, err := json.Marshal(idx)
	require.NoError(t, err)
	sig := ed25519.Sign(priv, data)
	return data, sig
}

// startIndexServer starts an httptest.Server serving index.json + index.json.sig.
// Returns the server and the base URL (the index URL is base+"/index.json").
func startIndexServer(t *testing.T, indexData, sigData []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(indexData)
		case "/index.json.sig":
			_, _ = w.Write(sigData)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchAndVerifyIndex_Valid(t *testing.T) {
	t.Parallel()
	pub, priv := makeTestKey(t)

	releases := []IndexRelease{
		{
			Version:   "v2.0.0",
			Date:      "2026-01-01",
			Artifacts: []IndexArtifact{{Name: "magus_v2.0.0_linux_amd64.tar.gz", Platform: "linux/amd64"}},
		},
		{
			Version:   "v1.0.0",
			Date:      "2025-01-01",
			Artifacts: []IndexArtifact{{Name: "magus_v1.0.0_linux_amd64.tar.gz", Platform: "linux/amd64"}},
		},
	}
	indexData, sigData := buildIndex(t, priv, releases)
	srv := startIndexServer(t, indexData, sigData)

	opts := Options{PubKey: pub, HTTPClient: srv.Client(), DiscoveryURL: srv.URL + "/index.json"}
	idx, err := FetchAndVerifyIndex(context.Background(), opts)
	require.NoError(t, err)
	require.Equal(t, ReleaseIndex{
		SchemaVersion: 1,
		Releases:      releases,
	}, *idx)
}

func TestFetchAndVerifyIndex_BadSig(t *testing.T) {
	t.Parallel()
	pub, _ := makeTestKey(t) // generate pub but sign with a different key
	_, wrongPriv := makeTestKey(t)

	releases := []IndexRelease{{Version: "v1.0.0", Artifacts: []IndexArtifact{{Name: "f.tar.gz"}}}}
	indexData, _ := buildIndex(t, wrongPriv, releases)
	// Sign with wrong key so sig is for different key
	badSig := ed25519.Sign(wrongPriv, indexData)
	srv := startIndexServer(t, indexData, badSig)

	opts := Options{PubKey: pub, HTTPClient: srv.Client(), DiscoveryURL: srv.URL + "/index.json"}
	_, err := FetchAndVerifyIndex(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature check failed")
}

func TestFetchAndVerifyIndex_Unreachable(t *testing.T) {
	t.Parallel()
	pub, _ := makeTestKey(t)

	// Point at a server that immediately closes connections.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hijack and close without responding.
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", 500)
			return
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	t.Cleanup(srv.Close)

	opts := Options{PubKey: pub, HTTPClient: srv.Client(), DiscoveryURL: srv.URL + "/index.json"}
	_, err := FetchAndVerifyIndex(context.Background(), opts)
	require.Error(t, err, "unreachable index must return an error, not succeed")
	// Must not silently fall back to anything.
	assert.Contains(t, err.Error(), "unreachable")
}

func TestFetchAndVerifyIndex_NilPubKey(t *testing.T) {
	t.Parallel()
	opts := Options{PubKey: nil, DiscoveryURL: "http://127.0.0.1:0/index.json"}
	_, err := FetchAndVerifyIndex(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no public key")
}

func TestFetchAndVerifyIndex_WrongSchemaVersion(t *testing.T) {
	t.Parallel()
	pub, priv := makeTestKey(t)

	data, _ := json.Marshal(map[string]any{"schema_version": 99, "releases": []any{}})
	sig := ed25519.Sign(priv, data)
	srv := startIndexServer(t, data, sig)

	opts := Options{PubKey: pub, HTTPClient: srv.Client(), DiscoveryURL: srv.URL + "/index.json"}
	_, err := FetchAndVerifyIndex(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema_version")
}

func TestSelectRelease_Latest(t *testing.T) {
	t.Parallel()
	idx := &ReleaseIndex{
		SchemaVersion: 1,
		Releases: []IndexRelease{
			{Version: "v2.0.0", Artifacts: []IndexArtifact{{Name: "f.tar.gz"}}},
			{Version: "v1.0.0", Artifacts: []IndexArtifact{{Name: "f.tar.gz"}}},
		},
	}
	rel, err := SelectRelease(idx, "")
	require.NoError(t, err)
	require.Equal(t, IndexRelease{Version: "v2.0.0", Artifacts: []IndexArtifact{{Name: "f.tar.gz"}}}, *rel)
}

func TestSelectRelease_SkipsYanked(t *testing.T) {
	t.Parallel()
	idx := &ReleaseIndex{
		SchemaVersion: 1,
		Releases: []IndexRelease{
			{Version: "v3.0.0", Yanked: true, Artifacts: []IndexArtifact{{Name: "f.tar.gz"}}},
			{Version: "v2.0.0", Artifacts: []IndexArtifact{{Name: "f.tar.gz"}}},
		},
	}
	rel, err := SelectRelease(idx, "")
	require.NoError(t, err)
	require.Equal(t, IndexRelease{Version: "v2.0.0", Artifacts: []IndexArtifact{{Name: "f.tar.gz"}}}, *rel)
}

func TestSelectRelease_ByTag(t *testing.T) {
	t.Parallel()
	idx := &ReleaseIndex{
		SchemaVersion: 1,
		Releases: []IndexRelease{
			{Version: "v2.0.0", Artifacts: []IndexArtifact{{Name: "f.tar.gz"}}},
			{Version: "v1.0.0", Artifacts: []IndexArtifact{{Name: "g.tar.gz"}}},
		},
	}
	rel, err := SelectRelease(idx, "v1.0.0")
	require.NoError(t, err)
	require.Equal(t, IndexRelease{Version: "v1.0.0", Artifacts: []IndexArtifact{{Name: "g.tar.gz"}}}, *rel)
}

func TestSelectRelease_TagNotFound(t *testing.T) {
	t.Parallel()
	idx := &ReleaseIndex{
		SchemaVersion: 1,
		Releases:      []IndexRelease{{Version: "v1.0.0"}},
	}
	_, err := SelectRelease(idx, "v9.9.9")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSelectRelease_YankedTag(t *testing.T) {
	t.Parallel()
	idx := &ReleaseIndex{
		SchemaVersion: 1,
		Releases:      []IndexRelease{{Version: "v1.0.0", Yanked: true}},
	}
	_, err := SelectRelease(idx, "v1.0.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "yanked")
}

func TestSelectRelease_AllYanked(t *testing.T) {
	t.Parallel()
	idx := &ReleaseIndex{
		SchemaVersion: 1,
		Releases:      []IndexRelease{{Version: "v1.0.0", Yanked: true}},
	}
	_, err := SelectRelease(idx, "")
	require.Error(t, err)
}

func TestFindAssetsFromIndex_AllPresent(t *testing.T) {
	t.Parallel()
	assetName := fmt.Sprintf("magus_v1.0.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	rel := &IndexRelease{
		Version: "v1.0.0",
		Artifacts: []IndexArtifact{
			{Name: assetName},
			{Name: "SHA256SUMS"},
			{Name: "SHA256SUMS.sig"},
		},
	}
	assets, err := FindAssetsFromIndex(rel, assetName)
	require.NoError(t, err)
	require.Equal(t, Assets{
		Tarball: "https://github.com/egladman/magus/releases/download/v1.0.0/" + assetName,
		Sums:    "https://github.com/egladman/magus/releases/download/v1.0.0/SHA256SUMS",
		Sig:     "https://github.com/egladman/magus/releases/download/v1.0.0/SHA256SUMS.sig",
	}, assets)
}

func TestFindAssetsFromIndex_MissingTarball(t *testing.T) {
	t.Parallel()
	rel := &IndexRelease{
		Version: "v1.0.0",
		Artifacts: []IndexArtifact{
			{Name: "SHA256SUMS"},
			{Name: "SHA256SUMS.sig"},
		},
	}
	_, err := FindAssetsFromIndex(rel, "missing.tar.gz")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing.tar.gz")
}

func TestParseManifestValid(t *testing.T) {
	t.Parallel()
	hash := strings.Repeat("a", 64) // 64 hex chars = sha256
	data := fmt.Sprintf("version: v1.2.3\n%s  magus-linux-amd64.tar.gz\n", hash)
	m, err := ParseManifest([]byte(data))
	require.NoError(t, err)
	require.Equal(t, Manifest{
		Version: "v1.2.3",
		Hashes:  map[string]string{"magus-linux-amd64.tar.gz": hash},
	}, *m)
}

func TestParseManifestMissingVersion(t *testing.T) {
	t.Parallel()
	hash := strings.Repeat("b", 64)
	data := fmt.Sprintf("%s  file.tar.gz\n", hash)
	_, err := ParseManifest([]byte(data))
	assert.Error(t, err, "expected error for missing version")
}

func TestParseManifestBadSemver(t *testing.T) {
	t.Parallel()
	hash := strings.Repeat("c", 64)
	data := fmt.Sprintf("version: not-semver\n%s  file.tar.gz\n", hash)
	_, err := ParseManifest([]byte(data))
	assert.Error(t, err, "expected error for invalid semver")
}

func TestParseManifestShortHash(t *testing.T) {
	t.Parallel()
	data := "version: v1.0.0\ndeadbeef  file.tar.gz\n"
	_, err := ParseManifest([]byte(data))
	assert.Error(t, err, "expected error for short hash")
}

func TestParseManifestEmpty(t *testing.T) {
	t.Parallel()
	_, err := ParseManifest([]byte(""))
	assert.Error(t, err, "expected error for empty manifest")
}

func TestParseManifestCommentsSkipped(t *testing.T) {
	t.Parallel()
	hash := strings.Repeat("d", 64)
	data := fmt.Sprintf("# this is a comment\nversion: v2.0.0\n%s  file.tar.gz\n", hash)
	m, err := ParseManifest([]byte(data))
	require.NoError(t, err)
	require.Equal(t, Manifest{
		Version: "v2.0.0",
		Hashes:  map[string]string{"file.tar.gz": hash},
	}, *m)
}

func TestCompare(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 1, Compare("v1.2.0", "v1.1.0"))
	assert.Equal(t, 0, Compare("v1.0.0", "v1.0.0"))
	assert.Equal(t, -1, Compare("v0.9.9", "v1.0.0"))
	// A version that does not parse compares equal (0), never panicking.
	assert.Equal(t, 0, Compare("not-semver", "v1.0.0"))
	assert.Equal(t, 0, Compare("v1.0.0", "not-semver"))
}

func makeTarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Size:     int64(len(content)),
		Mode:     0o755,
	}
	require.NoError(t, tw.WriteHeader(hdr))
	_, err := tw.Write([]byte(content))
	require.NoError(t, err)
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func TestExtractBinaryFound(t *testing.T) {
	t.Parallel()
	binaryName := "magus"
	if runtime.GOOS == "windows" {
		binaryName = "magus.exe"
	}
	data := makeTarGz(t, binaryName, "fake binary content")
	r, err := ExtractBinary(data)
	require.NoError(t, err)
	assert.NotNil(t, r)
}

func TestExtractBinaryNotFound(t *testing.T) {
	t.Parallel()
	data := makeTarGz(t, "other-file", "content")
	_, err := ExtractBinary(data)
	assert.Error(t, err, "expected error when binary not in archive")
}

func TestExtractBinaryPathTraversal(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	_ = tw.WriteHeader(&tar.Header{
		Name:     "../evil",
		Typeflag: tar.TypeReg,
		Size:     5,
		Mode:     0o644,
	})
	_, _ = tw.Write([]byte("hello"))
	tw.Close()
	gw.Close()
	_, err := ExtractBinary(buf.Bytes())
	assert.Error(t, err, "expected error for path traversal in archive")
}

func TestFetchAndVerifyManifestValid(t *testing.T) {
	t.Parallel()
	pub, priv := makeTestKey(t)

	hash := strings.Repeat("e", 64)
	manifest := []byte(fmt.Sprintf("version: v1.0.0\n%s  file.tar.gz\n", hash))
	sig := ed25519.Sign(priv, manifest)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sums":
			w.Write(manifest)
		case "/sig":
			w.Write(sig)
		}
	}))
	defer srv.Close()

	m, err := FetchAndVerifyManifest(
		context.Background(), srv.URL+"/sums", srv.URL+"/sig",
		Options{PubKey: pub},
	)
	require.NoError(t, err)
	require.Equal(t, Manifest{
		Version: "v1.0.0",
		Hashes:  map[string]string{"file.tar.gz": hash},
	}, *m)
}

func TestFetchAndVerifyManifestBadSig(t *testing.T) {
	t.Parallel()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	hash := strings.Repeat("f", 64)
	manifest := []byte(fmt.Sprintf("version: v1.0.0\n%s  file.tar.gz\n", hash))
	badSig := []byte("not a valid signature at all xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sums":
			w.Write(manifest)
		case "/sig":
			w.Write(badSig)
		}
	}))
	defer srv.Close()

	_, err = FetchAndVerifyManifest(
		context.Background(), srv.URL+"/sums", srv.URL+"/sig",
		Options{PubKey: pub},
	)
	assert.Error(t, err, "expected signature failure")
}

func TestFetchAndVerifyTarballHashMismatch(t *testing.T) {
	t.Parallel()
	content := makeTarGz(t, "magus", "binary content")
	wrongHash := strings.Repeat("0", 64)
	m := &Manifest{
		Version: "v1.0.0",
		Hashes:  map[string]string{"asset.tar.gz": wrongHash},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer srv.Close()

	_, err := FetchAndVerifyTarball(context.Background(), srv.URL+"/asset", "asset.tar.gz", m, Options{})
	assert.Error(t, err, "expected hash mismatch error")
}

func TestFetchAndVerifyTarballMissingManifestEntry(t *testing.T) {
	t.Parallel()
	m := &Manifest{
		Version: "v1.0.0",
		Hashes:  map[string]string{},
	}
	_, err := FetchAndVerifyTarball(context.Background(), "http://unused", "asset.tar.gz", m, Options{})
	assert.Error(t, err, "expected error for missing manifest entry")
}

func TestResolveTargetPathWithBinDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path, err := ResolveTargetPath(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, filepath.Dir(path))
}

func TestResolveTargetPathBinDirNotDir(t *testing.T) {
	t.Parallel()
	f, err := os.CreateTemp(t.TempDir(), "not-a-dir")
	require.NoError(t, err)
	f.Close()
	_, err = ResolveTargetPath(f.Name())
	assert.Error(t, err, "expected error when --bin-dir points to a file")
}

func TestCheckParentWritable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := CheckParentWritable(filepath.Join(dir, "magus"))
	assert.NoError(t, err)
}

// TestDownloadVerifyNilPubKeyFallsBack verifies that Options with nil PubKey
// does not panic on ed25519.Verify. The nil guard returns an error instead.
func TestDownloadVerifyNilPubKeyFallsBack(t *testing.T) {
	t.Parallel()
	hash := strings.Repeat("a", 64)
	manifest := []byte(fmt.Sprintf("version: v1.0.0\n%s  file.tar.gz\n", hash))
	badSig := make([]byte, 64)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sums":
			w.Write(manifest)
		case "/sig":
			w.Write(badSig)
		}
	}))
	defer srv.Close()

	_, err := FetchAndVerifyManifest(
		context.Background(), srv.URL+"/sums", srv.URL+"/sig",
		Options{},
	)
	assert.Error(t, err, "expected error with nil PubKey")
}

// TestDiscoveryURLOverride proves that Options.DiscoveryURL (and by extension
// MAGUS_UPDATE_URL env var) routes discovery to an alternate server.
// This covers the private update channel use case.
func TestDiscoveryURLOverride(t *testing.T) {
	t.Parallel()
	pub, priv := makeTestKey(t)

	releases := []IndexRelease{
		{
			Version:   "v9.9.9",
			Date:      "2099-01-01",
			Artifacts: []IndexArtifact{{Name: "magus_v9.9.9_linux_amd64.tar.gz", Platform: "linux/amd64"}},
		},
	}
	indexData, sigData := buildIndex(t, priv, releases)

	// Serve from a custom (non-default) server to prove the override works.
	srv := startIndexServer(t, indexData, sigData)

	opts := Options{PubKey: pub, HTTPClient: srv.Client(), DiscoveryURL: srv.URL + "/index.json"}
	idx, err := FetchAndVerifyIndex(context.Background(), opts)
	require.NoError(t, err)
	require.Len(t, idx.Releases, 1)
	assert.Equal(t, "v9.9.9", idx.Releases[0].Version)
}

// TestDiscoveryURLFromEnv proves that MAGUS_UPDATE_URL is honoured when
// DiscoveryURL is not set in Options.
func TestDiscoveryURLFromEnv(t *testing.T) {
	// Not parallel: modifies env.
	pub, priv := makeTestKey(t)

	releases := []IndexRelease{
		{
			Version:   "v7.0.0",
			Date:      "2099-01-01",
			Artifacts: []IndexArtifact{{Name: "magus_v7.0.0_linux_amd64.tar.gz"}},
		},
	}
	indexData, sigData := buildIndex(t, priv, releases)
	srv := startIndexServer(t, indexData, sigData)

	t.Setenv("MAGUS_UPDATE_URL", srv.URL+"/index.json")

	opts := Options{PubKey: pub, HTTPClient: srv.Client()} // DiscoveryURL intentionally empty
	idx, err := FetchAndVerifyIndex(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, "v7.0.0", idx.Releases[0].Version)
}

// TestDowngradeRefused proves that FetchAndVerifyIndex + SelectRelease alone
// does not enforce the version constraint (that is the cmd layer's job), but
// that the downgrade detection logic in Compare works correctly as the building
// block.
func TestDowngradeRefused_IndexAdvertisesLowerVersion(t *testing.T) {
	t.Parallel()
	pub, priv := makeTestKey(t)

	// Index advertises only v0.1.0 - lower than a running v1.0.0.
	releases := []IndexRelease{
		{
			Version:   "v0.1.0",
			Date:      "2025-01-01",
			Artifacts: []IndexArtifact{{Name: "magus_v0.1.0_linux_amd64.tar.gz"}},
		},
	}
	indexData, sigData := buildIndex(t, priv, releases)
	srv := startIndexServer(t, indexData, sigData)

	opts := Options{PubKey: pub, HTTPClient: srv.Client(), DiscoveryURL: srv.URL + "/index.json"}
	idx, err := FetchAndVerifyIndex(context.Background(), opts)
	require.NoError(t, err)

	rel, err := SelectRelease(idx, "")
	require.NoError(t, err)
	require.Equal(t, "v0.1.0", rel.Version)

	// The cmd layer checks: if index advertises a lower version than running, refuse.
	// Compare(indexVersion, runningVersion) == -1 means downgrade.
	runningVersion := "v1.0.0"
	assert.Equal(t, -1, Compare(rel.Version, runningVersion),
		"index at v0.1.0 vs running v1.0.0 must be detected as a downgrade")
}

func TestPrintUpdateStatus(t *testing.T) {
	t.Parallel()
	// PrintUpdateStatus writes to stdout; verify it does not panic for all three branches.
	// Capture is intentionally omitted - the test only asserts no panic.
	PrintUpdateStatus("v2.0.0", "v1.0.0") // newer available
	PrintUpdateStatus("v1.0.0", "v1.0.0") // up to date
	PrintUpdateStatus("v0.9.0", "v1.0.0") // running newer
}

func TestDefaultUserBinDir(t *testing.T) {
	t.Parallel()
	dir := DefaultUserBinDir()
	assert.NotEmpty(t, dir)
	assert.Contains(t, dir, "bin")
}

func TestDefaultUserManDir(t *testing.T) {
	t.Parallel()
	dir := DefaultUserManDir()
	assert.NotEmpty(t, dir)
	assert.Contains(t, dir, "man")
}

func TestEnsureDir(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dir := filepath.Join(base, "a", "b", "c")
	require.NoError(t, EnsureDir(dir))
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	// Idempotent: calling again must not error.
	require.NoError(t, EnsureDir(dir))
}

func TestCheckFileWritable_Writable(t *testing.T) {
	t.Parallel()
	f, err := os.CreateTemp(t.TempDir(), "writable-*")
	require.NoError(t, err)
	f.Close()
	assert.NoError(t, CheckFileWritable(f.Name()))
}

// TestFetchAndVerifyTarball_OK is an integration-style test covering the
// full SHA-256 verification path with a real hash.
func TestFetchAndVerifyTarball_OK(t *testing.T) {
	t.Parallel()
	binaryName := "magus"
	if runtime.GOOS == "windows" {
		binaryName = "magus.exe"
	}
	content := makeTarGz(t, binaryName, "real binary bytes")
	sum := sha256.Sum256(content)
	assetName := "magus_v1.0.0_linux_amd64.tar.gz"
	m := &Manifest{
		Version: "v1.0.0",
		Hashes:  map[string]string{assetName: hex.EncodeToString(sum[:])},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer srv.Close()

	r, err := FetchAndVerifyTarball(context.Background(), srv.URL+"/asset", assetName, m, Options{})
	require.NoError(t, err)
	assert.NotNil(t, r)
}
