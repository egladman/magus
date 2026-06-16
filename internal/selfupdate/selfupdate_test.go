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
)

// ── ParseManifest ──────────────────────────────────────────────────────────────

func TestParseManifestValid(t *testing.T) {
	t.Parallel()
	hash := strings.Repeat("a", 64) // 64 hex chars = sha256
	data := fmt.Sprintf("version: v1.2.3\n%s  magus-linux-amd64.tar.gz\n", hash)
	m, err := ParseManifest([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "v1.2.3" {
		t.Errorf("Version = %q, want v1.2.3", m.Version)
	}
	if got := m.Hashes["magus-linux-amd64.tar.gz"]; got != hash {
		t.Errorf("Hashes[...] = %q, want %q", got, hash)
	}
}

func TestParseManifestMissingVersion(t *testing.T) {
	t.Parallel()
	hash := strings.Repeat("b", 64)
	data := fmt.Sprintf("%s  file.tar.gz\n", hash)
	_, err := ParseManifest([]byte(data))
	if err == nil {
		t.Fatal("expected error for missing version, got nil")
	}
}

func TestParseManifestBadSemver(t *testing.T) {
	t.Parallel()
	hash := strings.Repeat("c", 64)
	data := fmt.Sprintf("version: not-semver\n%s  file.tar.gz\n", hash)
	_, err := ParseManifest([]byte(data))
	if err == nil {
		t.Fatal("expected error for invalid semver, got nil")
	}
}

func TestParseManifestShortHash(t *testing.T) {
	t.Parallel()
	data := "version: v1.0.0\ndeadbeef  file.tar.gz\n"
	_, err := ParseManifest([]byte(data))
	if err == nil {
		t.Fatal("expected error for short hash, got nil")
	}
}

func TestParseManifestEmpty(t *testing.T) {
	t.Parallel()
	_, err := ParseManifest([]byte(""))
	if err == nil {
		t.Fatal("expected error for empty manifest, got nil")
	}
}

func TestParseManifestCommentsSkipped(t *testing.T) {
	t.Parallel()
	hash := strings.Repeat("d", 64)
	data := fmt.Sprintf("# this is a comment\nversion: v2.0.0\n%s  file.tar.gz\n", hash)
	m, err := ParseManifest([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "v2.0.0" {
		t.Errorf("Version = %q, want v2.0.0", m.Version)
	}
}

// ── FindAssets ─────────────────────────────────────────────────────────────────

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
	if err != nil {
		t.Fatal(err)
	}
	if assets.Tarball == "" || assets.Sums == "" || assets.Sig == "" {
		t.Errorf("one or more URLs empty: tarball=%q sums=%q sig=%q",
			assets.Tarball, assets.Sums, assets.Sig)
	}
}

func TestFindAssetsMissing(t *testing.T) {
	t.Parallel()
	rel := &GitHubRelease{TagName: "v1.0.0"}
	_, err := FindAssets(rel, "magus-linux-amd64.tar.gz")
	if err == nil {
		t.Fatal("expected error for missing assets, got nil")
	}
}

// ── Compare ────────────────────────────────────────────────────────────────────

func TestCompare(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.0", "v1.1.0", 1},
		{"v1.0.0", "v1.0.0", 0},
		{"v0.9.9", "v1.0.0", -1},
		{"not-semver", "v1.0.0", 0},
		{"v1.0.0", "not-semver", 0},
	}
	for _, tc := range cases {
		got := Compare(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// ── ExtractBinary ──────────────────────────────────────────────────────────────

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
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected non-nil reader")
	}
}

func TestExtractBinaryNotFound(t *testing.T) {
	t.Parallel()
	data := makeTarGz(t, "other-file", "content")
	_, err := ExtractBinary(data)
	if err == nil {
		t.Fatal("expected error when binary not in archive")
	}
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
	if err == nil {
		t.Fatal("expected error for path traversal in archive")
	}
}

// ── FetchRelease ──────────────────────────────────────────────────────────────

func TestFetchReleaseLatest(t *testing.T) {
	t.Parallel()
	rel := &GitHubRelease{TagName: "v3.0.0"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer srv.Close()

	got, err := FetchRelease(context.Background(), "", Options{APIBase: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if got.TagName != "v3.0.0" {
		t.Errorf("TagName = %q, want v3.0.0", got.TagName)
	}
}

func TestFetchReleaseHTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := FetchRelease(context.Background(), "", Options{APIBase: srv.URL})
	if err == nil {
		t.Fatal("expected error for HTTP 404, got nil")
	}
}

// ── FetchAndVerifyManifest ────────────────────────────────────────────────────

func TestFetchAndVerifyManifestValid(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

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
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "v1.0.0" {
		t.Errorf("Version = %q, want v1.0.0", m.Version)
	}
}

func TestFetchAndVerifyManifestBadSig(t *testing.T) {
	t.Parallel()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

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
	if err == nil {
		t.Fatal("expected signature failure, got nil")
	}
}

// ── FetchAndVerifyTarball ─────────────────────────────────────────────────────

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
	if err == nil {
		t.Fatal("expected hash mismatch error, got nil")
	}
}

func TestFetchAndVerifyTarballMissingManifestEntry(t *testing.T) {
	t.Parallel()
	m := &Manifest{
		Version: "v1.0.0",
		Hashes:  map[string]string{},
	}
	_, err := FetchAndVerifyTarball(context.Background(), "http://unused", "asset.tar.gz", m, Options{})
	if err == nil {
		t.Fatal("expected error for missing manifest entry, got nil")
	}
}

// ── ResolveTargetPath ─────────────────────────────────────────────────────────

func TestResolveTargetPathWithBinDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path, err := ResolveTargetPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("Dir(%q) = %q, want %q", path, filepath.Dir(path), dir)
	}
}

func TestResolveTargetPathBinDirNotDir(t *testing.T) {
	t.Parallel()
	f, err := os.CreateTemp(t.TempDir(), "not-a-dir")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, err = ResolveTargetPath(f.Name())
	if err == nil {
		t.Fatal("expected error when --bin-dir points to a file, got nil")
	}
}

// ── CheckParentWritable ───────────────────────────────────────────────────────

func TestCheckParentWritable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := CheckParentWritable(filepath.Join(dir, "magus"))
	if err != nil {
		t.Errorf("CheckParentWritable on writable dir: %v", err)
	}
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
	if err == nil {
		t.Fatal("expected error with nil PubKey, got nil")
	}
}
