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
	if err != nil {
		t.Fatal(err)
	}

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
	mux.HandleFunc("/repos/egladman/tack/releases/latest", fx.serveLatest)
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
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ── self update ───────────────────────────────────────────────────────────────

func TestSelfUpdate_NewerVersion(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	if err := selfUpdateCmd(context.Background(), []string{"--dry-run", "--yes"}); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestSelfUpdate_AlreadyUpToDate(t *testing.T) {
	fx := newTestFixture(t, "v0.3.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	err := selfUpdateCmd(context.Background(), []string{"--dry-run", "--yes"})
	if err == nil {
		t.Fatal("expected error for same version, got nil")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSelfUpdate_Downgrade(t *testing.T) {
	fx := newTestFixture(t, "v0.2.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	err := selfUpdateCmd(context.Background(), []string{"--dry-run", "--yes"})
	if err == nil {
		t.Fatal("expected error for downgrade, got nil")
	}
	if !strings.Contains(err.Error(), "older than current") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSelfUpdate_DowngradeForce(t *testing.T) {
	fx := newTestFixture(t, "v0.2.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	if err := selfUpdateCmd(context.Background(), []string{"--dry-run", "--yes", "--force"}); err != nil {
		t.Fatalf("expected forced downgrade to succeed in dry-run, got: %v", err)
	}
}

func TestSelfUpdate_BadSignature(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	badPub, _, _ := ed25519.GenerateKey(rand.Reader)
	overridePubKey = badPub

	err := selfUpdateCmd(context.Background(), []string{"--dry-run", "--yes"})
	if err == nil {
		t.Fatal("expected signature verification to fail, got nil")
	}
	if !strings.Contains(err.Error(), "signature check failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSelfUpdate_TamperedTarball(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/egladman/tack/releases/latest", fx.serveLatest)
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
	if err == nil {
		t.Fatal("expected sha256 mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSelfUpdate_BinDir(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	dir := t.TempDir()
	if err := selfUpdateCmd(context.Background(), []string{"--dry-run", "--yes", "--bin-dir", dir}); err != nil {
		t.Fatalf("expected success with --bin-dir, got: %v", err)
	}
}

func TestSelfUpdate_BinDirNotExist(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	err := selfUpdateCmd(context.Background(), []string{"--dry-run", "--yes", "--bin-dir", "/nonexistent/path/xyz"})
	if err == nil {
		t.Fatal("expected error for non-existent --bin-dir, got nil")
	}
}

func TestSelfUpdate_CheckOnly(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	if err := selfUpdateCmd(context.Background(), []string{"--check"}); err != nil {
		t.Fatalf("--check should succeed even without --yes, got: %v", err)
	}
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
	if err == nil {
		t.Fatal("expected error for unsafe archive path, got nil")
	}
	if strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("wrong error — should be path/not-found, got: %v", err)
	}
}

func TestCheckParentWritable(t *testing.T) {
	t.Run("writable dir succeeds", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "magus")
		if err := selfupdate.CheckParentWritable(path); err != nil {
			t.Fatalf("checkParentWritable on writable dir: %v", err)
		}
	})

	t.Run("missing parent errors cleanly", func(t *testing.T) {
		err := selfupdate.CheckParentWritable("/nonexistent-magus-test-path-xyz/magus")
		if err == nil {
			t.Fatal("expected error for missing parent dir, got nil")
		}
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
			if err == nil {
				t.Fatal("expected error on readonly dir, got nil")
			}
			if !strings.Contains(err.Error(), "is not writable") {
				t.Fatalf("error %q does not include the user-facing hint", err)
			}
		})
	}
}

func TestParseManifest_Valid(t *testing.T) {
	raw := "version: v1.2.3\n" +
		"aabbcc" + strings.Repeat("0", 58) + "  magus_v1.2.3_linux_amd64.tar.gz\n"
	_, err := selfupdate.ParseManifest([]byte(raw))
	if err != nil {
		t.Fatalf("valid manifest failed: %v", err)
	}
}

func TestParseManifest_MissingVersion(t *testing.T) {
	raw := "abc" + strings.Repeat("0", 61) + "  magus_v1.0.0_linux_amd64.tar.gz\n"
	_, err := selfupdate.ParseManifest([]byte(raw))
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("expected missing-version error, got: %v", err)
	}
}

func TestParseManifest_BadVersion(t *testing.T) {
	raw := "version: not-semver\n" +
		"aabbcc" + strings.Repeat("0", 58) + "  magus_v1.0.0_linux_amd64.tar.gz\n"
	_, err := selfupdate.ParseManifest([]byte(raw))
	if err == nil || !strings.Contains(err.Error(), "semver") {
		t.Fatalf("expected semver error, got: %v", err)
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v0.9.0", 1},
		{"v1.0.0", "v1.0.0", 0},
		{"v0.9.0", "v1.0.0", -1},
		{"unknown", "v1.0.0", 0},
		{"v1.0.0", "unknown", 0},
	}
	for _, tc := range tests {
		got := selfupdate.Compare(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("selfupdate.Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// ── self install ──────────────────────────────────────────────────────────────

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
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
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
	if err != nil {
		t.Fatalf("expected success (no version gate), got: %v", err)
	}
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
	if err != nil {
		t.Fatalf("expected success with non-existent --bin-dir, got: %v", err)
	}
}

func TestSelfInstall_ManpagesWritten(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping manpage write in short mode")
	}
	manDir := t.TempDir()
	if err := installManpages(manDir); err != nil {
		t.Fatalf("installManpages: %v", err)
	}
	entries, err := os.ReadDir(manDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one man page to be written")
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".1") {
			t.Errorf("unexpected file in man dir: %s", e.Name())
		}
	}
}

func TestSelfInstall_DefaultDirs(t *testing.T) {
	// Smoke-test that the XDG helpers return non-empty paths and don't panic.
	binDir := selfupdate.DefaultUserBinDir()
	if binDir == "" {
		t.Error("DefaultUserBinDir returned empty string")
	}
	manDir := selfupdate.DefaultUserManDir()
	if manDir == "" {
		t.Error("DefaultUserManDir returned empty string")
	}
	// They should both land under the same ~/.local prefix by default.
	t.Logf("DefaultUserBinDir = %s", binDir)
	t.Logf("DefaultUserManDir = %s", manDir)
}
