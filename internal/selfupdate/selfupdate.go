//go:build !noselfupdate

// Package selfupdate downloads, verifies, and installs magus release binaries.
//
// Discovery reads ONLY the site's index.json (gen/public/release/index.json),
// whose Ed25519 signature (index.json.sig) is verified against the pinned
// release key before any data is trusted. The GitHub API is not used. This
// eliminates the unauthenticated 60 req/hr rate limit that blocked CI.
//
// The discovery URL is overridable via Options.DiscoveryURL or the
// MAGUS_UPDATE_URL environment variable (env-only; there is no magus.yaml
// key). An organization that self-hosts the site gets a private update
// channel for free: point that URL at the hosted copy of index.json and
// index.json.sig.
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
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"

	json "github.com/egladman/magus/internal/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

// Options configures an update operation. Keys is required; verification fails
// closed when it is empty.
type Options struct {
	// Keys is the ring a signature may come from, normally ReleaseKeys. After the
	// signed index has been read it is narrowed by Keyring.Without(idx.Revoked), so a
	// key the publisher revoked cannot verify anything fetched afterwards.
	Keys         Keyring
	HTTPClient   *http.Client
	DiscoveryURL string // overrides DefaultDiscoveryURL; also MAGUS_UPDATE_URL env var
}

// checkKeys fails closed unless the ring holds at least one key of the right length.
// ed25519.Verify panics on any other non-nil length, so every call site that verifies
// a signature must check this first.
func checkKeys(ring Keyring) error {
	if len(ring) == 0 {
		return errors.New("no release key configured: pass selfupdate.ReleaseKeys via Options.Keys (or, if every key was revoked, download the release manually from the release site)")
	}
	for _, key := range ring {
		if len(key.Pub) != ed25519.PublicKeySize {
			return fmt.Errorf("release key %s is %d bytes, want %d", key.ID, len(key.Pub), ed25519.PublicKeySize)
		}
	}
	return nil
}

// httpClient returns the client every signed fetch uses. The transport floor matches
// what the install script already demands of curl (`--proto '=https' --proto-redir
// '=https' --tlsv1.2`, docs/gen/install), so the two paths that can install magus agree
// about what they will speak. Without this the client inherited Go's default, which is
// reasonable and unstated - not the same thing as chosen.
func (o Options) httpClient() *http.Client {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
		// A redirect may not leave https. The tarball 302s from github.com to
		// release-assets.githubusercontent.com, so redirects are on the normal path and
		// an http:// hop would be a silent downgrade mid-download.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if err := requireHTTPS(req.URL.String()); err != nil {
				return fmt.Errorf("refusing redirect: %w", err)
			}
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		},
	}
}

// requireHTTPS rejects a non-https source. The Ed25519 signature makes plaintext
// survivable, not acceptable: a downgrade nobody is told about is how every quiet failure
// in this path starts. A load error, never a warning.
//
// Loopback is exempt, the same carve-out Docker, pip and the Go module proxy make. A
// packet that never leaves the host has no network attacker to protect it from, and the
// exemption is what lets a local mirror - and this package's own tests - exercise the
// real code path instead of a weakened copy of it.
func requireHTTPS(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse %q: %w", rawURL, err)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("refusing %q: a signed source must be https, got %q", rawURL, u.Scheme)
}

// isLoopbackHost reports whether host names this machine.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
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
	SchemaVersion int `json:"schema_version"`
	// KeyID names the key that signed this file. It is a cross-check, never the thing
	// that selects a key: an attacker controls it exactly as much as the rest of the
	// payload, so it is compared against the key that actually verified.
	KeyID string `json:"key_id"`
	// Revoked lists fingerprints no signature may come from. It is only believed when
	// the index carrying it was itself signed by a key not on the list - which is what
	// a standby key buys, and what makes a revocation an attacker cannot forge.
	Revoked []string `json:"revoked,omitzero"`
	// ExpiresAt bounds the one hole revocation cannot close: an attacker holding a
	// compromised key serving an OLD index that names no revocation. Past it the client
	// refuses the file rather than trusting a stale one, turning an indefinite
	// compromise into a denial of service. RFC3339; empty means no bound.
	ExpiresAt string         `json:"expires_at,omitzero"`
	Releases  []IndexRelease `json:"releases"`
}

// IndexRelease represents one entry inside ReleaseIndex.Releases. The index
// JSON carries additional fields (date, notes, body, per-artifact platform/
// size/sha256) that selfupdate does not read: json.Unmarshal drops unknown
// keys silently, and integrity comes from the separately verified SHA256SUMS
// file, not from unauthenticated index metadata.
type IndexRelease struct {
	Version   string          `json:"version"`
	Yanked    bool            `json:"yanked,omitempty"`
	Artifacts []IndexArtifact `json:"artifacts"`
}

// IndexArtifact is one artifact line inside IndexRelease.Artifacts.
type IndexArtifact struct {
	Name string `json:"name"`
}

// FetchAndVerifyIndex fetches index.json, verifies its Ed25519 signature
// against opts.Keys, and returns the parsed index. The signature file is
// fetched from the same base URL with ".sig" appended.
//
// If the index is unreachable, FetchAndVerifyIndex returns an error and stops.
// There is no silent fallback.
func FetchAndVerifyIndex(ctx context.Context, opts Options) (*ReleaseIndex, error) {
	if err := checkKeys(opts.Keys); err != nil {
		return nil, err
	}
	indexURL := opts.discoveryURL()
	if err := requireHTTPS(indexURL); err != nil {
		return nil, err
	}
	sigURL := indexURL + ".sig"

	// Signature FIRST. It cannot be verified without the artifact - a detached signature is
	// computed over those bytes - but fetching it first fails cheap when it is missing
	// rather than after pulling the whole index, and the artifact host is not contacted
	// until the expected signature is already pinned locally.
	sigBytes, err := FetchLimited(ctx, sigURL, MaxSig, opts)
	if err != nil {
		return nil, fmt.Errorf("release index signature unreachable (%s): %w", sigURL, err)
	}
	indexBytes, err := FetchLimited(ctx, indexURL, MaxIndex, opts)
	if err != nil {
		return nil, fmt.Errorf("release index unreachable (%s): %w", indexURL, err)
	}
	signer, err := opts.Keys.Verify(indexBytes, sigBytes)
	if err != nil {
		return nil, fmt.Errorf("index signature check failed: %w", err)
	}

	var idx ReleaseIndex
	if err := json.Unmarshal(indexBytes, &idx); err != nil {
		return nil, fmt.Errorf("parse release index: %w", err)
	}
	if idx.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported release index schema_version %d (want 1)", idx.SchemaVersion)
	}
	if idx.KeyID != "" && idx.KeyID != signer.ID {
		return nil, fmt.Errorf("release index says it was signed by key %s but key %s is what verified it", idx.KeyID, signer.ID)
	}
	// A revoked key cannot vouch for its own standing. Refusing here is what forces a
	// real revocation to be signed by a DIFFERENT key - the standby one - which is the
	// only version of this an attacker holding the compromised key cannot produce.
	if slices.Contains(idx.Revoked, signer.ID) {
		return nil, fmt.Errorf("release index is signed by key %s, which the index itself revokes", signer.ID)
	}
	if err := checkNotExpired(idx.ExpiresAt); err != nil {
		return nil, err
	}
	if len(idx.Releases) == 0 {
		return nil, errors.New("release index contains no releases")
	}
	return &idx, nil
}

// checkNotExpired refuses an index past its declared lifetime. An unparsable
// expires_at is refused too: the field exists to bound how long a stale index can be
// replayed, so a client that cannot read it has no bound at all.
func checkNotExpired(expiresAt string) error {
	if expiresAt == "" {
		return nil
	}
	deadline, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return fmt.Errorf("release index expires_at %q is not RFC3339: %w", expiresAt, err)
	}
	if now := time.Now(); now.After(deadline) {
		return fmt.Errorf("release index expired at %s (%d days ago); it is republished on release, "+
			"so this is either a stale mirror or a replay - reinstall from https://eli.gladman.cc/magus/setup/",
			expiresAt, int(now.Sub(deadline).Hours()/24))
	}
	return nil
}

// SelectRelease returns the IndexRelease for the requested tag from idx.
// When tag is empty, the release with the highest valid semver Version among
// non-yanked entries is returned; positional order in the index is not
// trusted (an index that is not newest-first, whether by bug or tampering,
// must not select a stale release). Entries whose Version is not valid
// semver are rejected rather than considered.
func SelectRelease(idx *ReleaseIndex, tag string) (*IndexRelease, error) {
	if tag == "" {
		var best *IndexRelease
		for i := range idx.Releases {
			rel := &idx.Releases[i]
			if rel.Yanked || !semver.IsValid(rel.Version) {
				continue
			}
			if best == nil || semver.Compare(rel.Version, best.Version) > 0 {
				best = rel
			}
		}
		if best == nil {
			return nil, errors.New("no non-yanked release with a valid semver version found in index")
		}
		return best, nil
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

// FindAssets locates the tarball, checksum file, and signature file within an
// IndexRelease. Download URLs are derived from the release page URL pattern
// (GitHub release assets).
func FindAssets(rel *IndexRelease, assetName string) (Assets, error) {
	has := func(name string) bool {
		for _, a := range rel.Artifacts {
			if a.Name == name {
				return true
			}
		}
		return false
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
	if has(assetName) {
		a.Tarball = urlFor(assetName)
	}
	if has("SHA256SUMS") {
		a.Sums = urlFor("SHA256SUMS")
	}
	if has("SHA256SUMS.sig") {
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
	if err := checkKeys(opts.Keys); err != nil {
		return nil, err
	}
	// Signature first, for the reason given in FetchAndVerifyIndex.
	sigBytes, err := FetchLimited(ctx, sigURL, MaxSig, opts)
	if err != nil {
		return nil, fmt.Errorf("download SHA256SUMS.sig: %w", err)
	}
	sumsBytes, err := FetchLimited(ctx, sumsURL, MaxManifest, opts)
	if err != nil {
		return nil, fmt.Errorf("download SHA256SUMS: %w", err)
	}
	// SHA256SUMS names no signer - it is a sha256sum(1)-compatible file and stays one -
	// so the ring is tried. The caller has already narrowed it by the index's revoked[],
	// which is what stops a revoked key vouching for the artifact hashes.
	if _, err := opts.Keys.Verify(sumsBytes, sigBytes); err != nil {
		return nil, fmt.Errorf("signature check failed: SHA256SUMS.sig does not match SHA256SUMS: %w", err)
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

// compareParsed is Compare's logic plus the ok bit Compare itself drops: callers
// that need to tell "genuinely equal" apart from "could not parse" (PrintUpdateStatus,
// for a dev build's non-semver version) use this instead.
func compareParsed(a, b string) (cmp int, ok bool) {
	if !semver.IsValid(a) || !semver.IsValid(b) {
		return 0, false
	}
	return semver.Compare(a, b), true
}

// Compare returns -1, 0, or 1. Non-semver inputs are treated as equal.
func Compare(a, b string) int {
	cmp, _ := compareParsed(a, b)
	return cmp
}

// PrintUpdateStatus writes a one-line current-vs-available comparison.
func PrintUpdateStatus(tagName, currentVersion string) {
	cmp, ok := compareParsed(tagName, currentVersion)
	if !ok {
		fmt.Printf("cannot compare %s with %s: not a recognized version\n", currentVersion, tagName)
		return
	}
	switch cmp {
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
