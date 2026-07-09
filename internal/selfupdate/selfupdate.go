//go:build !noselfupdate

// Package selfupdate downloads, verifies, and installs magus release binaries.
//
// Discovery reads ONLY the site's index.json (gen/public/release/index.json),
// whose Ed25519 signature (index.json.sig) is verified against the pinned
// release key before any data is trusted. The GitHub API is not used. This
// eliminates the unauthenticated 60 req/hr rate limit that blocked CI.
//
// The discovery URL is overridable via Options.DiscoveryURL or the
// MAGUS_UPDATE_URL environment variable. An organisation that self-hosts the
// site (see magus.yaml selfupdate.discovery_url) gets a private update channel
// for free: point that URL at the hosted copy of index.json and index.json.sig.
//
// Artifact download URLs come from the manifest inside the index; they may
// point at GitHub release assets. That is artifact hosting, not discovery, and
// is unaffected by this change.
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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egladman/magus/internal/retry"
	"golang.org/x/mod/semver"
)

// Release coordinates and size caps for the self-update download.
const (
	ReleaseOwner = "egladman"
	ReleaseRepo  = "magus"
	MaxManifest  = 64 << 10  // 64 KB
	MaxSig       = 1 << 10   // 1 KB
	MaxTarball   = 200 << 20 // 200 MB
	MaxIndex     = 1 << 20   // 1 MB

	// DefaultDiscoveryURL is the canonical URL for the machine-readable release
	// index. Override via Options.DiscoveryURL or MAGUS_UPDATE_URL.
	DefaultDiscoveryURL = "https://eli.gladman.cc/magus/public/release/index.json"
)

// Options configures an update operation. PubKey is required; manifest verification fails closed when nil.
type Options struct {
	PubKey       ed25519.PublicKey
	HTTPClient   *http.Client
	DiscoveryURL string // overrides DefaultDiscoveryURL; also MAGUS_UPDATE_URL env var
}

func (o Options) httpClient() *http.Client {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (o Options) discoveryURL() string {
	if o.DiscoveryURL != "" {
		return o.DiscoveryURL
	}
	if env := os.Getenv("MAGUS_UPDATE_URL"); env != "" {
		return env
	}
	return DefaultDiscoveryURL
}

// ReleaseIndex is the JSON shape of gen/public/release/index.json (schema_version 1).
// Schema is frozen at birth; additive changes only.
type ReleaseIndex struct {
	SchemaVersion int            `json:"schema_version"`
	Releases      []IndexRelease `json:"releases"`
}

// IndexRelease represents one entry inside ReleaseIndex.Releases.
type IndexRelease struct {
	Version   string          `json:"version"`
	Date      string          `json:"date"`
	Yanked    bool            `json:"yanked,omitempty"`
	Artifacts []IndexArtifact `json:"artifacts"`
	// Notes and Body are present but not consumed by selfupdate.
}

// IndexArtifact is one artifact line inside IndexRelease.Artifacts.
type IndexArtifact struct {
	Name     string `json:"name"`
	Platform string `json:"platform,omitempty"`
	Size     string `json:"size,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	// DownloadURL is not in the JSON schema; it is synthesised from the
	// release page URL (artifact hosting on GitHub releases).
	DownloadURL string `json:"-"`
}

// FetchAndVerifyIndex fetches index.json, verifies its Ed25519 signature
// against opts.PubKey, and returns the parsed index. The signature file is
// fetched from the same base URL with ".sig" appended.
//
// If the index is unreachable, FetchAndVerifyIndex returns an error and stops.
// There is no silent fallback.
func FetchAndVerifyIndex(ctx context.Context, opts Options) (*ReleaseIndex, error) {
	if opts.PubKey == nil {
		return nil, errors.New("no public key: set Options.PubKey or embed a release key")
	}
	indexURL := opts.discoveryURL()
	sigURL := indexURL + ".sig"

	indexBytes, err := FetchLimited(ctx, indexURL, MaxIndex, opts)
	if err != nil {
		return nil, fmt.Errorf("release index unreachable (%s): %w", indexURL, err)
	}
	sigBytes, err := FetchLimited(ctx, sigURL, MaxSig, opts)
	if err != nil {
		return nil, fmt.Errorf("release index signature unreachable (%s): %w", sigURL, err)
	}
	if !ed25519.Verify(opts.PubKey, indexBytes, sigBytes) {
		return nil, errors.New("index signature check failed: index.json.sig does not match index.json")
	}

	var idx ReleaseIndex
	if err := json.Unmarshal(indexBytes, &idx); err != nil {
		return nil, fmt.Errorf("parse release index: %w", err)
	}
	if idx.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported release index schema_version %d (want 1)", idx.SchemaVersion)
	}
	if len(idx.Releases) == 0 {
		return nil, errors.New("release index contains no releases")
	}
	return &idx, nil
}

// SelectRelease returns the IndexRelease for the requested tag from idx.
// When tag is empty, the newest non-yanked release is returned.
func SelectRelease(idx *ReleaseIndex, tag string) (*IndexRelease, error) {
	if tag == "" {
		// Releases are newest-first per the index schema.
		for i := range idx.Releases {
			if !idx.Releases[i].Yanked {
				return &idx.Releases[i], nil
			}
		}
		return nil, errors.New("no non-yanked release found in index")
	}
	for i := range idx.Releases {
		if idx.Releases[i].Version == tag {
			if idx.Releases[i].Yanked {
				return nil, fmt.Errorf("release %s has been yanked", tag)
			}
			return &idx.Releases[i], nil
		}
	}
	return nil, fmt.Errorf("release %s not found in index", tag)
}

// Manifest holds the verified release version and asset hashes.
type Manifest struct {
	Version string
	Hashes  map[string]string // asset filename -> lowercase hex sha256
}

// Assets holds the download URLs for a release's tarball, SHA256SUMS manifest, and Ed25519 signature.
type Assets struct {
	Tarball string
	Sums    string
	Sig     string
}

// FindAssetsFromIndex locates the tarball, checksum file, and signature file
// within an IndexRelease. Download URLs are derived from the release page URL
// pattern (GitHub release assets).
func FindAssetsFromIndex(rel *IndexRelease, assetName string) (Assets, error) {
	// Build a name->artifact map for O(n) lookup.
	byName := make(map[string]IndexArtifact, len(rel.Artifacts))
	for _, a := range rel.Artifacts {
		byName[a.Name] = a
	}

	// Derive download URLs: GitHub release assets for this version.
	// Artifact hosting may be GitHub regardless of where the index is served.
	ghBase := fmt.Sprintf(
		"https://github.com/%s/%s/releases/download/%s",
		ReleaseOwner, ReleaseRepo, rel.Version,
	)
	urlFor := func(name string) string {
		return ghBase + "/" + name
	}

	var a Assets
	if _, ok := byName[assetName]; ok {
		a.Tarball = urlFor(assetName)
	}
	if _, ok := byName["SHA256SUMS"]; ok {
		a.Sums = urlFor("SHA256SUMS")
	}
	if _, ok := byName["SHA256SUMS.sig"]; ok {
		a.Sig = urlFor("SHA256SUMS.sig")
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
			rel.Version, strings.Join(missing, ", "))
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
		fmt.Printf("update available: %s -> %s\n", currentVersion, tagName)
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
