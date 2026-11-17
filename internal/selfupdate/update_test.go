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
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testIndexExpiry is far enough out that no fixture index expires while a test runs.
const testIndexExpiry = "2999-01-01T00:00:00Z"

// testFixture holds everything needed to stand up a local index server and
// artifact server for integration tests of Update.
type testFixture struct {
	pub     ed25519.PublicKey
	priv    ed25519.PrivateKey
	tag     string
	tarball []byte

	// indexSrv serves /index.json and /index.json.sig.
	// artifactSrv serves /tarball, /sums, and /sig.
	indexSrv    *httptest.Server
	artifactSrv *httptest.Server
}

// newTestFixture stands up a local index+artifact pair advertising tag. The
// optional manifestVersion argument lets a test make the signed SHA256SUMS
// "version:" header diverge from the index's Version, to exercise the
// mismatch guard in Update; when omitted it defaults to tag.
func newTestFixture(t *testing.T, tag string, manifestVersion ...string) *testFixture {
	t.Helper()
	return newIndexFixture(t, tag, func(served IndexRelease) ReleaseIndex {
		return ReleaseIndex{SchemaVersion: 1, ExpiresAt: testIndexExpiry, Releases: []IndexRelease{served}}
	}, manifestVersion...)
}

// newIndexFixture is newTestFixture with the index shaped by shape, which receives
// the one release whose assets the fixture serves.
func newIndexFixture(t *testing.T, tag string, shape func(served IndexRelease) ReleaseIndex, manifestVersion ...string) *testFixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	sumsVersion := tag
	if len(manifestVersion) > 0 {
		sumsVersion = manifestVersion[0]
	}

	tarball := makeStandInTarball(t)
	assetName := fmt.Sprintf("magus_%s_%s_%s_static.tar.gz", tag, runtime.GOOS, releaseArch())
	sum := sha256.Sum256(tarball)
	sumHex := hex.EncodeToString(sum[:])

	fx := &testFixture{
		pub:     pub,
		priv:    priv,
		tag:     tag,
		tarball: tarball,
	}

	// Start the artifact server first so we know its URL for the index.
	fx.artifactSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tarball":
			w.Write(fx.tarball)
		case "/sums":
			// A minimal SHA256SUMS file with the version header.
			fmt.Fprintf(w, "version: %s\n%s  %s\n", sumsVersion, sumHex, assetName)
		case "/sig":
			// Sign the same manifest body.
			manifest := []byte(fmt.Sprintf("version: %s\n%s  %s\n", sumsVersion, sumHex, assetName))
			sig := ed25519.Sign(priv, manifest)
			w.Write(sig)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fx.artifactSrv.Close)

	// Build the release index: the artifact download URLs point at artifactSrv.
	artifactBase := fx.artifactSrv.URL
	idx := shape(IndexRelease{
		Version: tag,
		Artifacts: []IndexArtifact{
			{Name: assetName},
			{Name: "SHA256SUMS"},
			{Name: "SHA256SUMS.sig"},
		},
	})
	// We need to override FindAssets's URL computation so it uses the test
	// server. We do that by intercepting the GitHub download path via an
	// http.Client transport that redirects github.com -> artifactSrv.
	_ = artifactBase // used via transport below

	indexData, err := json.Marshal(idx)
	require.NoError(t, err)
	indexSig := ed25519.Sign(priv, indexData)

	fx.indexSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			w.Header().Set("Content-Type", "application/json")
			w.Write(indexData)
		case "/index.json.sig":
			w.Write(indexSig)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fx.indexSrv.Close)

	return fx
}

// activate wires the fixture into the package-level overrides and restores them on cleanup.
// The HTTP client is configured with a transport that redirects GitHub release asset
// URLs (github.com/egladman/magus/releases/download/...) to the artifact server.
func (fx *testFixture) activate(t *testing.T) {
	t.Helper()
	prevKey := overrideKeys
	prevClient := overrideClient
	prevBase := overrideDiscoveryURL
	t.Cleanup(func() {
		overrideKeys = prevKey
		overrideClient = prevClient
		overrideDiscoveryURL = prevBase
	})
	overrideKeys = Keyring{{ID: KeyID(fx.pub), State: KeyActive, Pub: fx.pub}}
	overrideDiscoveryURL = fx.indexSrv.URL + "/index.json"

	// Build a client that redirects GitHub release asset fetches to the artifact server.
	artifactBase := fx.artifactSrv.URL
	assetName := fmt.Sprintf("magus_%s_%s_%s_static.tar.gz", fx.tag, runtime.GOOS, releaseArch())
	ghPrefix := fmt.Sprintf("https://github.com/egladman/magus/releases/download/%s/", fx.tag)
	transport := &redirectTransport{
		base:        http.DefaultTransport,
		matchPrefix: ghPrefix,
		replacements: map[string]string{
			ghPrefix + assetName:        artifactBase + "/tarball",
			ghPrefix + "SHA256SUMS":     artifactBase + "/sums",
			ghPrefix + "SHA256SUMS.sig": artifactBase + "/sig",
		},
		// Also allow through index server URLs unchanged.
		indexBase: fx.indexSrv.URL,
	}
	overrideClient = &http.Client{Transport: transport}
}

// redirectTransport rewrites GitHub release asset URLs to the test artifact server.
type redirectTransport struct {
	base         http.RoundTripper
	matchPrefix  string
	replacements map[string]string
	indexBase    string
}

func (t *redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	u := r.URL.String()
	if dest, ok := t.replacements[u]; ok {
		req2, err := http.NewRequestWithContext(r.Context(), r.Method, dest, r.Body)
		if err != nil {
			return nil, err
		}
		return t.base.RoundTrip(req2)
	}
	return t.base.RoundTrip(r)
}

// testVersion is the running version runUpdate reports to Update.
var testVersion string

// setVersion sets the running version runUpdate reports for the duration of the test.
func setVersion(t *testing.T, v string) {
	t.Helper()
	prev := testVersion
	t.Cleanup(func() { testVersion = prev })
	testVersion = v
}

// runUpdate runs req as the running build setVersion named, approving any install.
func runUpdate(t *testing.T, req Request) error {
	t.Helper()
	req.Current = testVersion
	req.Confirm = func(string, string, string) error { return nil }
	_, err := Update(context.Background(), req)
	return err
}

// makeStandInTarball returns a valid .tar.gz containing a tiny stand-in magus binary.
func makeStandInTarball(t *testing.T) []byte {
	t.Helper()
	binaryName := "magus"
	if runtime.GOOS == "windows" {
		binaryName = "magus.exe"
	}
	content := []byte("#!/bin/sh\necho stand-in magus\n")

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

	require.NoError(t, runUpdate(t, Request{DryRun: true}))
}

func TestSelfUpdate_AlreadyUpToDate(t *testing.T) {
	fx := newTestFixture(t, "v0.3.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	err := runUpdate(t, Request{DryRun: true})
	require.Error(t, err, "expected error for same version")
	assert.Contains(t, err.Error(), "already running")
	// The remedy must hand back a flag that actually exists: --force, not --uf.Force
	// (the field name on the generated bind struct, which is not a flag).
	assert.Contains(t, err.Error(), "--force")
	assert.NotContains(t, err.Error(), "uf.")
}

// TestSelfUpdate_DowngradeRefused proves that when the index advertises a lower
// version than the running binary (without --version being explicit), the command
// refuses with a downgrade error.
func TestSelfUpdate_DowngradeRefused(t *testing.T) {
	fx := newTestFixture(t, "v0.2.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	err := runUpdate(t, Request{DryRun: true})
	require.Error(t, err, "expected error for downgrade")
	// Must not silently install the older version.
	assert.Contains(t, err.Error(), "refusing downgrade")
	assert.Contains(t, err.Error(), "--version")
	assert.Contains(t, err.Error(), "--force")
	assert.NotContains(t, err.Error(), "uf.")
}

// TestSelfUpdate_DowngradeWithExplicitVersion proves that an explicit --version
// pointing at a lower release still requires --force (not silently allowed).
func TestSelfUpdate_DowngradeWithExplicitVersion(t *testing.T) {
	fx := newTestFixture(t, "v0.2.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	err := runUpdate(t, Request{DryRun: true, Version: "v0.2.0"})
	require.Error(t, err, "expected error for explicit-version downgrade without --force")
	assert.Contains(t, err.Error(), "older than current")
	assert.Contains(t, err.Error(), "--force")
	assert.NotContains(t, err.Error(), "uf.")
}

func TestSelfUpdate_DowngradeForce(t *testing.T) {
	fx := newTestFixture(t, "v0.2.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	require.NoError(t, runUpdate(t, Request{DryRun: true, Force: true}),
		"expected forced downgrade to succeed in dry-run")
}

// newCandidateFixture serves v0.5.0-rc.1's assets beside a stable v0.4.3, with the
// candidate under Prereleases as release-index publishes it, or under Releases as
// an index written before that split did.
func newCandidateFixture(t *testing.T, underReleases bool) *testFixture {
	t.Helper()
	return newIndexFixture(t, "v0.5.0-rc.1", func(rc IndexRelease) ReleaseIndex {
		stable := IndexRelease{Version: "v0.4.3"}
		if underReleases {
			return ReleaseIndex{SchemaVersion: 1, ExpiresAt: testIndexExpiry, Releases: []IndexRelease{rc, stable}}
		}
		return ReleaseIndex{
			SchemaVersion: 1,
			ExpiresAt:     testIndexExpiry,
			Releases:      []IndexRelease{stable},
			Prereleases:   []IndexRelease{rc},
		}
	})
}

func TestSelfUpdate_AutoSelectSkipsPrerelease(t *testing.T) {
	for _, underReleases := range []bool{false, true} {
		t.Run(fmt.Sprintf("underReleases=%v", underReleases), func(t *testing.T) {
			newCandidateFixture(t, underReleases).activate(t)
			setVersion(t, "v0.4.3")

			err := runUpdate(t, Request{DryRun: true})
			require.EqualError(t, err, "magus self update: already running v0.4.3 (use --force to reinstall)")
		})
	}
}

func TestSelfUpdate_ExplicitPrereleaseInstalls(t *testing.T) {
	newCandidateFixture(t, false).activate(t)
	setVersion(t, "v0.4.3")

	require.NoError(t, runUpdate(t, Request{DryRun: true, Version: "v0.5.0-rc.1"}))
}

func TestSelfUpdate_RunningPrereleaseRefusalNamesTheChannel(t *testing.T) {
	newCandidateFixture(t, false).activate(t)
	setVersion(t, "v0.5.0-rc.1")

	err := runUpdate(t, Request{DryRun: true})
	require.EqualError(t, err, "you are running prerelease v0.5.0-rc.1 and automatic updates follow stable releases only, "+
		"the newest stable release is v0.4.3, "+
		"name a later prerelease with --version, or use --version v0.4.3 --force to return to stable")
}

// TestSelfUpdate_UnknownVersionRefusesAutoSelect proves that a dev build
// (version == "unknown") does not silently auto-install the newest advertised
// release: with no --version and no --force it must refuse, since there is no
// running-version baseline to compare against for a downgrade check.
func TestSelfUpdate_UnknownVersionRefusesAutoSelect(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "unknown")

	err := runUpdate(t, Request{DryRun: true})
	require.Error(t, err, "expected refusal for unversioned dev build without --version or --force")
	assert.Contains(t, err.Error(), "unversioned")
	assert.Contains(t, err.Error(), "--version")
	assert.Contains(t, err.Error(), "--force")
	assert.NotContains(t, err.Error(), "uf.")
}

// TestSelfUpdate_UnknownVersionWithExplicitVersionSucceeds proves that passing
// --version opts a dev build in to an explicit install.
func TestSelfUpdate_UnknownVersionWithExplicitVersionSucceeds(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "unknown")

	require.NoError(t, runUpdate(t, Request{DryRun: true, Version: "v0.4.0"}),
		"expected explicit --version to bypass the unversioned-build guard")
}

// TestSelfUpdate_UnknownVersionWithForceSucceeds proves that --force opts a
// dev build in to an install without requiring --version.
func TestSelfUpdate_UnknownVersionWithForceSucceeds(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "unknown")

	require.NoError(t, runUpdate(t, Request{DryRun: true, Force: true}),
		"expected --force to bypass the unversioned-build guard")
}

// TestSelfUpdate_ManifestVersionMismatch proves that a signed SHA256SUMS whose
// "version:" header disagrees with the index's advertised Version is refused,
// rather than trusted just because the tarball hash matches. assetName is
// built from the index Version, so a stale or tampered manifest for a
// different version must not be silently accepted.
func TestSelfUpdate_ManifestVersionMismatch(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0", "v0.9.9")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	err := runUpdate(t, Request{DryRun: true})
	require.Error(t, err, "expected refusal on index/manifest version mismatch")
	assert.Contains(t, err.Error(), "advertises")
}

func TestSelfUpdate_BadSignature(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	badPub, _, _ := ed25519.GenerateKey(rand.Reader)
	overrideKeys = Keyring{{ID: KeyID(badPub), State: KeyActive, Pub: badPub}}

	err := runUpdate(t, Request{DryRun: true})
	require.Error(t, err, "expected signature verification to fail")
	// The bad key will fail on the index.json.sig check.
	assert.Contains(t, err.Error(), "signature check failed")
}

func TestSelfUpdate_IndexUnreachable(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	// Serve a server that refuses connections.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", 500)
			return
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	t.Cleanup(srv.Close)

	prevKey := overrideKeys
	prevClient := overrideClient
	prevBase := overrideDiscoveryURL
	t.Cleanup(func() {
		overrideKeys = prevKey
		overrideClient = prevClient
		overrideDiscoveryURL = prevBase
	})
	overrideKeys = Keyring{{ID: KeyID(pub), State: KeyActive, Pub: pub}}
	overrideClient = srv.Client()
	overrideDiscoveryURL = srv.URL + "/index.json"
	setVersion(t, "v0.3.0")

	err = runUpdate(t, Request{DryRun: true})
	require.Error(t, err, "unreachable index must fail, not silently succeed")
	// Must mention the index fetch, not some other error.
	assert.Contains(t, err.Error(), "release index")
}

func TestSelfUpdate_BinDir(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	dir := t.TempDir()
	require.NoError(t, runUpdate(t, Request{DryRun: true, BinDir: dir}),
		"expected success with --bin-dir")
}

func TestSelfUpdate_BinDirNotExist(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	err := runUpdate(t, Request{DryRun: true, BinDir: "/nonexistent/path/xyz"})
	assert.Error(t, err, "expected error for non-existent --bin-dir")
}

func TestSelfUpdate_CheckOnly(t *testing.T) {
	fx := newTestFixture(t, "v0.4.0")
	fx.activate(t)
	setVersion(t, "v0.3.0")

	require.NoError(t, runUpdate(t, Request{Check: true}),
		"--check should succeed even without --yes")
}

// TestArchToken verifies the asset architecture token, which is runtime.GOARCH
// everywhere except 32-bit ARM. There the token must resolve to one of the exactly
// two names CD publishes (armv6, armv7); echoing an unpublished level back would
// name an asset that does not exist.
func TestArchToken(t *testing.T) {
	cases := []struct {
		name   string
		goarch string
		goarm  string
		want   string
	}{
		{"amd64 passes through", "amd64", "", "amd64"},
		{"arm64 is not a GOARM arch", "arm64", "7", "arm64"},
		{"arm with GOARM=7", "arm", "7", "armv7"},
		{"arm with GOARM=6", "arm", "6", "armv6"},
		{"GOARM options are trimmed", "arm", "7,softfloat", "armv7"},
		{"GOARM=6 with options", "arm", "6,softfloat", "armv6"},
		// Levels CD does not publish must not be echoed back as an asset name.
		{"GOARM=5 clamps to the published armv6", "arm", "5", "armv6"},
		{"an unknown future level clamps to armv6", "arm", "9", "armv6"},
		{"no recorded level falls back to armv6", "arm", "", "armv6"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, archToken(c.goarch, c.goarm))
		})
	}
}

// TestArchTokenOnlyPublishedARMNames pins the invariant that no GOARM value can
// produce a name outside the set CD uploads, so a `magus self update` on any 32-bit
// ARM build resolves to an asset that exists.
func TestArchTokenOnlyPublishedARMNames(t *testing.T) {
	published := map[string]bool{"armv6": true, "armv7": true}
	for _, goarm := range []string{"", "5", "6", "7", "8", "9", "7,softfloat", "6,softfloat", "garbage"} {
		require.Truef(t, published[archToken("arm", goarm)],
			"GOARM=%q resolved to an unpublished asset name %q", goarm, archToken("arm", goarm))
	}
}

// TestReleaseArchMatchesGOARCHOffARM guards that the token only ever deviates from
// runtime.GOARCH on 32-bit ARM, so no other platform's `magus self update` can start
// requesting an asset name the release does not publish.
func TestReleaseArchMatchesGOARCHOffARM(t *testing.T) {
	if runtime.GOARCH == "arm" {
		t.Skip("host is 32-bit ARM; the deviation is expected there")
	}
	require.Equal(t, runtime.GOARCH, releaseArch())
}

// TestSelfUpdate_DiscoveryHost pins the precedence discoveryHost must mirror from
// Options.discoveryURL: an explicit Options.DiscoveryURL wins, then
// MAGUS_UPDATE_URL, then the default site; the confirmation prompt always names
// the host that will actually be fetched from.
func TestSelfUpdate_DiscoveryHost(t *testing.T) {
	t.Run("explicit DiscoveryURL wins", func(t *testing.T) {
		got := discoveryHost(Options{DiscoveryURL: "https://mirror.example.com/index.json"})
		assert.Equal(t, "mirror.example.com", got)
	})

	t.Run("falls back to MAGUS_UPDATE_URL", func(t *testing.T) {
		t.Setenv("MAGUS_UPDATE_URL", "https://private.example.org/index.json")
		got := discoveryHost(Options{})
		assert.Equal(t, "private.example.org", got)
	})

	t.Run("falls back to the default site", func(t *testing.T) {
		t.Setenv("MAGUS_UPDATE_URL", "")
		got := discoveryHost(Options{})
		want, err := url.Parse(DefaultDiscoveryURL)
		require.NoError(t, err)
		assert.Equal(t, want.Host, got)
	})
}
