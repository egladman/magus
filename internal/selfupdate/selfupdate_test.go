//go:build !noselfmanage

package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
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

func TestParseManifestValid(t *testing.T) {
	t.Parallel()
	hash := strings.Repeat("a", 64) // 64 hex chars = sha256
	data := fmt.Sprintf("version: v1.2.3\n%s  magus-linux-amd64.tar.gz\n", hash)
	m, err := ParseManifest([]byte(data))
	require.NoError(t, err)
	assert.Equal(t, "v1.2.3", m.Version)
	assert.Equal(t, hash, m.Hashes["magus-linux-amd64.tar.gz"])
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
	assert.Equal(t, "v2.0.0", m.Version)
}

func TestFindAssetsAllPresent(t *testing.T) {
	t.Parallel()
	rel := &GitHubRelease{
		TagName: "v1.0.0",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{Name: "magus-linux-amd64.tar.gz", BrowserDownloadURL: "http://example.com/bin.tar.gz"},
			{Name: "SHA256SUMS", BrowserDownloadURL: "http://example.com/sums"},
			{Name: "SHA256SUMS.sig", BrowserDownloadURL: "http://example.com/sig"},
		},
	}
	assets, err := FindAssets(rel, "magus-linux-amd64.tar.gz")
	require.NoError(t, err)
	assert.NotEmpty(t, assets.Tarball)
	assert.NotEmpty(t, assets.Sums)
	assert.NotEmpty(t, assets.Sig)
}

func TestFindAssetsMissing(t *testing.T) {
	t.Parallel()
	rel := &GitHubRelease{TagName: "v1.0.0"}
	_, err := FindAssets(rel, "magus-linux-amd64.tar.gz")
	assert.Error(t, err, "expected error for missing assets")
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

func TestFetchReleaseLatest(t *testing.T) {
	t.Parallel()
	rel := &GitHubRelease{TagName: "v3.0.0"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer srv.Close()

	got, err := FetchRelease(context.Background(), "", Options{APIBase: srv.URL})
	require.NoError(t, err)
	assert.Equal(t, "v3.0.0", got.TagName)
}

func TestFetchReleaseHTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := FetchRelease(context.Background(), "", Options{APIBase: srv.URL})
	assert.Error(t, err, "expected error for HTTP 404")
}

func TestFetchAndVerifyManifestValid(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

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
	assert.Equal(t, "v1.0.0", m.Version)
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
