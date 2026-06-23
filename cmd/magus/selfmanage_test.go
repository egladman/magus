//go:build selfmanage

package main

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

	"github.com/egladman/magus/internal/selfupdate"
)

// testFixture holds everything needed to stand up a fake GitHub release server.
type testFixture struct {
	pub      ed25519.PublicKey
	priv     ed25519.PrivateKey
	tag      string
	tarball  []byte
	manifest string
	sig      []byte
	srv      *httptest.Server
}

func newTestFixture(t *testing.T, tag string) *testFixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	tarball := makeFakeTarball(t)
	assetName := fmt.Sprintf("magus_%s_%s_%s.tar.gz", tag, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(tarball)
	manifest := fmt.Sprintf("version: %s\n%s  %s\n", tag, hex.EncodeToString(sum[:]), assetName)
	sig := ed25519.Sign(priv, []byte(manifest))

	fx := &testFixture{
		pub:      pub,
		priv:     priv,
		tag:      tag,
		tarball:  tarball,
		manifest: manifest,
		sig:      sig,
	}
	fx.srv = httptest.NewServer(fx.handler())
	t.Cleanup(fx.srv.Close)
	return fx
}

func (fx *testFixture) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/egladman/magus/releases/latest", fx.serveLatest)
	mux.HandleFunc("/tarball", fx.serveTarball)
	mux.HandleFunc("/sums", fx.serveSums)
	mux.HandleFunc("/sig", fx.serveSig)
	return mux
}

func (fx *testFixture) serveLatest(w http.ResponseWriter, r *http.Request) {
	base := "http://" + r.Host
	assetName := fmt.Sprintf("magus_%s_%s_%s.tar.gz", fx.tag, runtime.GOOS, runtime.GOARCH)
	rel := map[string]any{
		"tag_name": fx.tag,
		"assets": []map[string]any{
			{"name": assetName, "browser_download_url": base + "/tarball"},
			{"name": "SHA256SUMS", "browser_download_url": base + "/sums"},
			{"name": "SHA256SUMS.sig", "browser_download_url": base + "/sig"},
		},
	}
	json.NewEncoder(w).Encode(rel)
}

func (fx *testFixture) serveTarball(w http.ResponseWriter, r *http.Request) {
	w.Write(fx.tarball)
}

func (fx *testFixture) serveSums(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte(fx.manifest))
}

func (fx *testFixture) serveSig(w http.ResponseWriter, r *http.Request) {
	w.Write(fx.sig)
}

// activate wires the fixture into the package-level overrides and restores them on cleanup.
func (fx *testFixture) activate(t *testing.T) {
	t.Helper()
	prevKey := overridePubKey
	prevClient := overrideClient
	prevBase := overrideAPIBase
	t.Cleanup(func() {
		overridePubKey = prevKey
		overrideClient = prevClient
		overrideAPIBase = prevBase
	})
	overridePubKey = fx.pub
	overrideClient = fx.srv.Client()
	overrideAPIBase = fx.srv.URL
}

// setVersion sets the package-level version variable for the duration of the test.
func setVersion(t *testing.T, v string) {
	t.Helper()
	prev := version
	t.Cleanup(func() { version = prev })
	version = v
}

// makeFakeTarball returns a valid .tar.gz containing a tiny fake magus binary.
func makeFakeTarball(t *testing.T) []byte {
	t.Helper()
	binaryName := "magus"
	if runtime.GOOS == "windows" {
		binaryName = "magus.exe"
	}
	content := []byte("#!/bin/sh\necho fake magus\n")

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	hdr := &tar.Header{
		Name:     binaryName,
		Typeflag: tar.TypeReg,
		Mode:     0o755,
		Size:     int64(len(content)),
	}
	require.NoError(t, tw.WriteHeader(hdr))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

func TestSelfUpdate_NewerVersion(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	assert.NoError(t, selfUpdateCmd(context.Background(), []string{"--dry-run", "--yes"}))
}

func TestSelfUpdate_AlreadyUpToDate(t *testing.T) {
	fx := newTestFixture(t, "v0.3.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	err := selfUpdateCmd(context.Background(), []string{"--dry-run", "--yes"})
	require.Error(t, err, "expected error for same version")
	assert.Contains(t, err.Error(), "already running")
}

func TestSelfUpdate_Downgrade(t *testing.T) {
	fx := newTestFixture(t, "v0.2.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	err := selfUpdateCmd(context.Background(), []string{"--dry-run", "--yes"})
	require.Error(t, err, "expected error for downgrade")
	assert.Contains(t, err.Error(), "older than current")
}

func TestSelfUpdate_DowngradeForce(t *testing.T) {
	fx := newTestFixture(t, "v0.2.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	assert.NoError(t, selfUpdateCmd(context.Background(), []string{"--dry-run", "--yes", "--force"}),
		"expected forced downgrade to succeed in dry-run")
}

func TestSelfUpdate_BadSignature(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	badPub, _, _ := ed25519.GenerateKey(rand.Reader)
	overridePubKey = badPub

	err := selfUpdateCmd(context.Background(), []string{"--dry-run", "--yes"})
	require.Error(t, err, "expected signature verification to fail")
	assert.Contains(t, err.Error(), "signature check failed")
}

func TestSelfUpdate_TamperedTarball(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/egladman/magus/releases/latest", fx.serveLatest)
	mux.HandleFunc("/tarball", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("this is not the real tarball"))
	})
	mux.HandleFunc("/sums", fx.serveSums)
	mux.HandleFunc("/sig", fx.serveSig)
	badSrv := httptest.NewServer(mux)
	t.Cleanup(badSrv.Close)
	overrideClient = badSrv.Client()
	overrideAPIBase = badSrv.URL

	err := selfUpdateCmd(context.Background(), []string{"--dry-run", "--yes"})
	require.Error(t, err, "expected sha256 mismatch error")
	assert.Contains(t, err.Error(), "SHA-256 mismatch")
}

func TestSelfUpdate_BinDir(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	dir := t.TempDir()
	assert.NoError(t, selfUpdateCmd(context.Background(), []string{"--dry-run", "--yes", "--bin-dir", dir}),
		"expected success with --bin-dir")
}

func TestSelfUpdate_BinDirNotExist(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	err := selfUpdateCmd(context.Background(), []string{"--dry-run", "--yes", "--bin-dir", "/nonexistent/path/xyz"})
	assert.Error(t, err, "expected error for non-existent --bin-dir")
}

func TestSelfUpdate_CheckOnly(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	assert.NoError(t, selfUpdateCmd(context.Background(), []string{"--check"}),
		"--check should succeed even without --yes")
}

func TestSelfUpdate_UnsafeArchivePath(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	content := []byte("evil")
	hdr := &tar.Header{
		Name:     "../../../etc/passwd",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(content)),
	}
	tw.WriteHeader(hdr)
	tw.Write(content)
	tw.Close()
	gw.Close()
	evilTarball := buf.Bytes()

	_, err := selfupdate.ExtractBinary(evilTarball)
	require.Error(t, err, "expected error for unsafe archive path")
	assert.NotContains(t, err.Error(), "SHA-256", "wrong error — should be path/not-found")
}

func TestCheckParentWritable(t *testing.T) {
	t.Run("writable dir succeeds", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "magus")
		assert.NoError(t, selfupdate.CheckParentWritable(path))
	})

	t.Run("missing parent errors cleanly", func(t *testing.T) {
		err := selfupdate.CheckParentWritable("/nonexistent-magus-test-path-xyz/magus")
		assert.Error(t, err, "expected error for missing parent dir")
	})

	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		t.Run("readonly dir produces permission hint", func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0o555); err != nil {
				t.Skipf("cannot make dir readonly: %v", err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

			path := filepath.Join(dir, "magus")
			err := selfupdate.CheckParentWritable(path)
			require.Error(t, err, "expected error on readonly dir")
			assert.Contains(t, err.Error(), "is not writable", "error does not include the user-facing hint")
		})
	}
}

func TestParseManifest_Valid(t *testing.T) {
	raw := "version: v1.2.3\n" +
		"aabbcc" + strings.Repeat("0", 58) + "  magus_v1.2.3_linux_amd64.tar.gz\n"
	_, err := selfupdate.ParseManifest([]byte(raw))
	assert.NoError(t, err, "valid manifest failed")
}

func TestParseManifest_MissingVersion(t *testing.T) {
	raw := "abc" + strings.Repeat("0", 61) + "  magus_v1.0.0_linux_amd64.tar.gz\n"
	_, err := selfupdate.ParseManifest([]byte(raw))
	require.Error(t, err, "expected missing-version error")
	assert.Contains(t, err.Error(), "version")
}

func TestParseManifest_BadVersion(t *testing.T) {
	raw := "version: not-semver\n" +
		"aabbcc" + strings.Repeat("0", 58) + "  magus_v1.0.0_linux_amd64.tar.gz\n"
	_, err := selfupdate.ParseManifest([]byte(raw))
	require.Error(t, err, "expected semver error")
	assert.Contains(t, err.Error(), "semver")
}

func TestCompare(t *testing.T) {
	assertCompare := func(a, b string, want int) {
		t.Run(fmt.Sprintf("%s_vs_%s", a, b), func(t *testing.T) {
			assert.Equal(t, want, selfupdate.Compare(a, b))
		})
	}

	assertCompare("v1.0.0", "v0.9.0", 1)
	assertCompare("v1.0.0", "v1.0.0", 0)
	assertCompare("v0.9.0", "v1.0.0", -1)
	assertCompare("unknown", "v1.0.0", 0)
	assertCompare("v1.0.0", "unknown", 0)
}

func TestSelfInstall_FreshInstall(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "unknown") // bootstrap binary has no version

	binDir := t.TempDir()
	manDir := t.TempDir()

	err := selfInstallCmd(context.Background(), []string{
		"--dry-run", "--yes",
		"--bin-dir", binDir,
		"--man-dir", manDir,
	})
	assert.NoError(t, err)
}

func TestSelfInstall_SkipsVersionGate(t *testing.T) {
	// install should succeed even when the running version == the target version,
	// because the version gate is intentionally absent from self install.
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.4.0") // same version — would fail self update

	binDir := t.TempDir()
	err := selfInstallCmd(context.Background(), []string{
		"--dry-run", "--yes",
		"--bin-dir", binDir,
	})
	assert.NoError(t, err, "expected success (no version gate)")
}

func TestSelfInstall_BinDirCreated(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)

	// Use a subdirectory that doesn't exist yet; EnsureDir should create it.
	base := t.TempDir()
	binDir := filepath.Join(base, "new", "bin")

	err := selfInstallCmd(context.Background(), []string{
		"--dry-run", "--yes",
		"--bin-dir", binDir,
	})
	assert.NoError(t, err, "expected success with non-existent --bin-dir")
}

func TestSelfInstall_ManpagesWritten(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping manpage write in short mode")
	}
	manDir := t.TempDir()
	require.NoError(t, installManpages(manDir))
	entries, err := os.ReadDir(manDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "expected at least one man page to be written")
	for _, e := range entries {
		assert.True(t, strings.HasSuffix(e.Name(), ".1"), "unexpected file in man dir: %s", e.Name())
	}
}

func TestSelfInstall_DefaultDirs(t *testing.T) {
	// Smoke-test that the XDG helpers return non-empty paths and don't panic.
	binDir := selfupdate.DefaultUserBinDir()
	assert.NotEmpty(t, binDir, "DefaultUserBinDir returned empty string")
	manDir := selfupdate.DefaultUserManDir()
	assert.NotEmpty(t, manDir, "DefaultUserManDir returned empty string")
	// They should both land under the same ~/.local prefix by default.
	t.Logf("DefaultUserBinDir = %s", binDir)
	t.Logf("DefaultUserManDir = %s", manDir)
}
