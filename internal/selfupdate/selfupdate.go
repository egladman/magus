//go:build !noselfmanage

// Package selfupdate downloads, verifies, and installs magus release binaries.
package selfupdate

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/retry"
	"golang.org/x/mod/semver"
)

// Release coordinates and size caps for the self-update download.
const (
	ReleaseOwner = "egladman"
	ReleaseRepo  = "tack"
	MaxManifest  = 64 << 10  // 64 KB
	MaxSig       = 1 << 10   // 1 KB
	MaxTarball   = 200 << 20 // 200 MB
)

// Options configures an update operation. PubKey is required; manifest verification fails closed when nil.
type Options struct {
	PubKey     ed25519.PublicKey
	HTTPClient *http.Client
	APIBase    string
}

func (o Options) httpClient() *http.Client {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (o Options) apiBase() string {
	if o.APIBase != "" {
		return o.APIBase
	}
	return "https://api.github.com"
}

// GitHubRelease is the shape of a GitHub release API response.
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// Manifest holds the verified release version and asset hashes.
type Manifest struct {
	Version string
	Hashes  map[string]string // asset filename → lowercase hex sha256
}

// FetchRelease fetches the release metadata for a tag (or the latest release
// when tag is empty).
func FetchRelease(ctx context.Context, tag string, opts Options) (*GitHubRelease, error) {
	var u string
	if tag == "" {
		u = fmt.Sprintf("%s/repos/%s/%s/releases/latest",
			opts.apiBase(), ReleaseOwner, ReleaseRepo)
	} else {
		u = fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s",
			opts.apiBase(), ReleaseOwner, ReleaseRepo, tag)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := opts.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %s", resp.Status)
	}
	var rel GitHubRelease
	if err := codec.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release JSON: %w", err)
	}
	if rel.TagName == "" {
		return nil, errors.New("release has no tag_name")
	}
	return &rel, nil
}

// Assets holds the download URLs for a release's tarball, SHA256SUMS manifest, and Ed25519 signature.
type Assets struct {
	Tarball string
	Sums    string
	Sig     string
}

// FindAssets locates the tarball, checksum file, and signature file in a
// release by their expected names.
func FindAssets(rel *GitHubRelease, assetName string) (Assets, error) {
	var a Assets
	for _, ra := range rel.Assets {
		switch ra.Name {
		case assetName:
			a.Tarball = ra.BrowserDownloadURL
		case "SHA256SUMS":
			a.Sums = ra.BrowserDownloadURL
		case "SHA256SUMS.sig":
			a.Sig = ra.BrowserDownloadURL
		}
	}
	var missing []string
	if a.Tarball == "" {
		missing = append(missing, assetName)
	}
	if a.Sums == "" {
		missing = append(missing, "SHA256SUMS")
	}
	if a.Sig == "" {
		missing = append(missing, "SHA256SUMS.sig")
	}
	if len(missing) > 0 {
		return a, fmt.Errorf("release %s is missing required assets: %s",
			rel.TagName, strings.Join(missing, ", "))
	}
	return a, nil
}

// FetchAndVerifyManifest downloads and Ed25519-verifies the SHA256SUMS file.
func FetchAndVerifyManifest(ctx context.Context, sumsURL, sigURL string, opts Options) (*Manifest, error) {
	if opts.PubKey == nil {
		return nil, errors.New("no public key: set Options.PubKey or embed a release key")
	}
	sumsBytes, err := FetchLimited(ctx, sumsURL, MaxManifest, opts)
	if err != nil {
		return nil, fmt.Errorf("download SHA256SUMS: %w", err)
	}
	sigBytes, err := FetchLimited(ctx, sigURL, MaxSig, opts)
	if err != nil {
		return nil, fmt.Errorf("download SHA256SUMS.sig: %w", err)
	}
	if !ed25519.Verify(opts.PubKey, sumsBytes, sigBytes) {
		return nil, errors.New("signature check failed: SHA256SUMS.sig does not match SHA256SUMS")
	}
	return ParseManifest(sumsBytes)
}

// ParseManifest decodes a SHA256SUMS file into an Manifest.
func ParseManifest(data []byte) (*Manifest, error) {
	m := &Manifest{Hashes: make(map[string]string)}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if after, ok := strings.CutPrefix(line, "version:"); ok {
			m.Version = strings.TrimSpace(after)
			continue
		}
		if strings.ContainsRune(line, ':') && !strings.Contains(line, "  ") {
			continue
		}
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			continue
		}
		hashHex, name := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if len(hashHex) != hex.EncodedLen(sha256.Size) {
			return nil, fmt.Errorf("invalid SHA-256 hash length for %q: got %d chars, want %d",
				name, len(hashHex), hex.EncodedLen(sha256.Size))
		}
		m.Hashes[name] = hashHex
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan manifest: %w", err)
	}
	if m.Version == "" {
		return nil, errors.New("manifest is missing the required version: header")
	}
	if !semver.IsValid(m.Version) {
		return nil, fmt.Errorf("manifest version %q is not a valid semver string", m.Version)
	}
	return m, nil
}

// FetchAndVerifyTarball downloads and SHA-256 verifies the tarball; returns a reader for the binary inside.
func FetchAndVerifyTarball(ctx context.Context, url, assetName string, m *Manifest, opts Options) (io.Reader, error) {
	expectedHex, ok := m.Hashes[assetName]
	if !ok {
		return nil, fmt.Errorf("manifest contains no entry for %s", assetName)
	}
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		return nil, fmt.Errorf("decode expected hash: %w", err)
	}

	data, err := FetchLimited(ctx, url, MaxTarball, opts)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", assetName, err)
	}

	sum := sha256.Sum256(data)
	if !bytes.Equal(sum[:], expected) {
		return nil, fmt.Errorf("SHA-256 mismatch for %s\n  expected: %s\n  got:      %s",
			assetName, expectedHex, hex.EncodeToString(sum[:]))
	}
	return ExtractBinary(data)
}

// ExtractBinary reads the magus binary from a .tar.gz archive.
func ExtractBinary(tarGz []byte) (io.Reader, error) {
	gr, err := gzip.NewReader(bytes.NewReader(tarGz))
	if err != nil {
		return nil, fmt.Errorf("open gzip stream: %w", err)
	}
	defer func() { _ = gr.Close() }()

	binaryName := "magus"
	if runtime.GOOS == "windows" {
		binaryName = "magus.exe"
	}

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		cleaned := filepath.ToSlash(filepath.Clean(hdr.Name))
		if strings.HasPrefix(cleaned, "/") || strings.Contains(cleaned, "..") {
			return nil, fmt.Errorf("archive contains unsafe path: %q", hdr.Name)
		}
		if filepath.Base(hdr.Name) != binaryName {
			continue
		}
		buf, err := io.ReadAll(io.LimitReader(tr, MaxTarball+1))
		if err != nil {
			return nil, fmt.Errorf("read binary from archive: %w", err)
		}
		if int64(len(buf)) > MaxTarball {
			return nil, fmt.Errorf("binary exceeds maximum allowed size (%d bytes)", MaxTarball)
		}
		return bytes.NewReader(buf), nil
	}
	return nil, fmt.Errorf("archive does not contain %q", binaryName)
}

// FetchLimited fetches url with retry and enforces a byte limit.
func FetchLimited(ctx context.Context, url string, maxBytes int64, opts Options) ([]byte, error) {
	var data []byte
	err := retry.Do(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := opts.httpClient().Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP %s fetching %s", resp.Status, url)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}
		if int64(len(b)) > maxBytes {
			return fmt.Errorf("response from %s exceeded %d bytes", url, maxBytes)
		}
		data = b
		return nil
	}, retry.WithDelay(2*time.Second), retry.WithMaxDelay(30*time.Second))
	return data, err
}

// Compare returns -1, 0, or 1. Non-semver inputs are treated as equal.
func Compare(a, b string) int {
	if !semver.IsValid(a) || !semver.IsValid(b) {
		return 0
	}
	return semver.Compare(a, b)
}

// PrintUpdateStatus writes a one-line current-vs-available comparison.
func PrintUpdateStatus(tagName, currentVersion string) {
	switch Compare(tagName, currentVersion) {
	case 1:
		fmt.Printf("update available: %s → %s\n", currentVersion, tagName)
	case 0:
		fmt.Printf("already up to date (%s)\n", currentVersion)
	case -1:
		fmt.Printf("current version %s is newer than latest release %s\n", currentVersion, tagName)
	}
}

// ResolveTargetPath returns the path where the binary should be installed.
func ResolveTargetPath(binDir string) (string, error) {
	binaryName := "magus"
	if runtime.GOOS == "windows" {
		binaryName = "magus.exe"
	}
	if binDir != "" {
		abs, err := filepath.Abs(binDir)
		if err != nil {
			return "", fmt.Errorf("resolve --bin-dir: %w", err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("--bin-dir %s: %w", abs, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("--bin-dir %s is not a directory", abs)
		}
		return filepath.Join(abs, binaryName), nil
	}
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return resolveExePath(exePath), nil
}

// CheckFileWritable probes a path with O_WRONLY.
func CheckFileWritable(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf(
				"binary at %s is not writable by the current user\n"+
					"  hint: re-run with elevated privileges, or reinstall via your package manager",
				path,
			)
		}
		return fmt.Errorf("check writability of %s: %w", path, err)
	}
	_ = f.Close()
	return nil
}

// DefaultUserBinDir returns the XDG-aware default installation directory (~/.local/bin).
func DefaultUserBinDir() string {
	if data := os.Getenv("XDG_DATA_HOME"); data != "" {
		return filepath.Join(filepath.Dir(data), "bin")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin")
}

// DefaultUserManDir returns the XDG-aware default directory for section-1 man pages (~/.local/share/man/man1).
func DefaultUserManDir() string {
	if data := os.Getenv("XDG_DATA_HOME"); data != "" {
		return filepath.Join(data, "man", "man1")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "man", "man1")
}

// EnsureDir creates dir and all parents with 0755 permissions; no-op if already exists.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	return nil
}

// CheckParentWritable probes the parent directory of path by creating a temp file.
func CheckParentWritable(path string) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".magus-writable-*")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf(
				"directory %s is not writable by the current user\n"+
					"  hint: re-run from an elevated prompt, or pass --bin-dir to a writable location",
				dir,
			)
		}
		return fmt.Errorf("check writability of %s: %w", dir, err)
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return nil
}
