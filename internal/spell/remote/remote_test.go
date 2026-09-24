package remote

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/oci"
	"github.com/egladman/magus/types"
)

// registry is enough of the OCI distribution API to push and pull: a token endpoint,
// the two-step blob upload, and manifests addressed by tag or digest. It serves TLS
// because the oci client addresses every registry as https.
type registry struct {
	srv       *httptest.Server
	mu        sync.Mutex
	blobs     map[string][]byte
	manifests map[string][]byte
	pulls     int
	// user and pass, when set, make the token endpoint refuse any other credential,
	// the way a private repository does.
	user, pass string
}

func newRegistry(t *testing.T) *registry {
	t.Helper()
	r := &registry{blobs: map[string][]byte{}, manifests: map[string][]byte{}}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *registry) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := req.URL.Path
	last := p[strings.LastIndex(p, "/")+1:]
	switch {
	case p == "/v2/":
		w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+req.Host+`/token",service="`+req.Host+`"`)
		w.WriteHeader(http.StatusUnauthorized)
	case p == "/token":
		if u, pw, _ := req.BasicAuth(); r.user != "" && (u != r.user || pw != r.pass) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"token":"fake"}`))
	case strings.HasPrefix(p, "/upload/"):
		body, _ := io.ReadAll(req.Body)
		r.blobs[req.URL.Query().Get("digest")] = body
		w.WriteHeader(http.StatusCreated)
	case strings.HasSuffix(p, "/blobs/uploads/"):
		w.Header().Set("Location", "/upload/1")
		w.WriteHeader(http.StatusAccepted)
	case strings.Contains(p, "/blobs/"):
		b, ok := r.blobs[last]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if req.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(b)
	case strings.Contains(p, "/manifests/"):
		if req.Method == http.MethodPut {
			body, _ := io.ReadAll(req.Body)
			r.manifests[last] = body
			r.manifests[digest.FromBytes(body).String()] = body
			w.WriteHeader(http.StatusCreated)
			return
		}
		r.pulls++
		b, ok := r.manifests[last]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(b)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (r *registry) host() string { return strings.TrimPrefix(r.srv.URL, "https://") }

func (r *registry) pullCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pulls
}

// spellDir writes a spell directory with a nested file.
func spellDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, entryFile), []byte("export fun mgs_getName() > str { return \"x\"; }\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "lib"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib", "util.buzz"), []byte("fun util() > void {}\n"), 0o644))
	return dir
}

// allTracked reports every path it is asked about as tracked.
type allTracked struct{}

func (allTracked) TrackedFiles(_ context.Context, _ string, paths []string) ([]string, error) {
	return paths, nil
}

// trackedOnly reports the paths it holds, the way a backend answers for an index.
type trackedOnly []string

func (t trackedOnly) TrackedFiles(_ context.Context, _ string, paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		if slices.Contains(t, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

// publish pushes a spell directory under tag v1 and returns the pin for it.
func publish(t *testing.T, reg *registry, dir string) Ref {
	t.Helper()
	dest := oci.Reference{Registry: reg.host(), Repository: "team/spells/x", Tag: "v1"}
	content, err := Build(t.Context(), dir, allTracked{}, Provenance{Title: "x"})
	require.NoError(t, err)
	c := &oci.Client{HTTP: reg.srv.Client(), Username: reg.user, Password: reg.pass}
	d, err := c.Push(t.Context(), dest, content)
	require.NoError(t, err)
	ref, err := Pinned(reg.host()+"/team/spells/x", d)
	require.NoError(t, err)
	return ref
}

// fixedCommit answers every FindCommit with one commit, the way a checkout at a fixed
// revision does.
type fixedCommit types.Commit

func (c fixedCommit) FindCommit(context.Context, string, string) (types.Commit, error) {
	return types.Commit(c), nil
}

// RemoteURL answers as a checkout with no remote configured does.
func (c fixedCommit) RemoteURL(context.Context, string, string) (string, error) {
	return "", types.ErrVCSUnsupported
}

// withRemote adds a default remote to fixedCommit.
type withRemote struct {
	fixedCommit
	remote string
}

func (w withRemote) RemoteURL(context.Context, string, string) (string, error) { return w.remote, nil }

func TestPinned(t *testing.T) {
	t.Parallel()
	d := digest.FromBytes([]byte("m"))
	got, err := Pinned("ghcr.io/egladman/magus/spells/cursor", d)
	require.NoError(t, err)
	assert.Equal(t, Ref{
		Import: "ghcr.io/egladman/magus/spells/cursor",
		OCI:    oci.Reference{Registry: "ghcr.io", Repository: "egladman/magus/spells/cursor", Digest: d},
	}, got)

	_, err = Pinned("spells/harness/cursor", d)
	require.ErrorContains(t, err, "not a registry path")

	_, err = Pinned("ghcr.io/egladman/magus/spells/cursor", digest.Digest("sha256:abc"))
	require.Error(t, err, "a malformed digest pins nothing")
}

func TestPackIsDeterministic(t *testing.T) {
	t.Parallel()
	a, b := spellDir(t), spellDir(t)
	old := time.Unix(1_000_000, 0)
	require.NoError(t, os.Chtimes(filepath.Join(b, entryFile), old, old))
	require.NoError(t, os.Chmod(filepath.Join(b, "lib", "util.buzz"), 0o600))
	pa, err := Pack(t.Context(), a, allTracked{})
	require.NoError(t, err)
	pb, err := Pack(t.Context(), b, allTracked{})
	require.NoError(t, err)
	assert.Equal(t, pa, pb, "mtime and mode do not reach the layer")

	files, err := tarFiles(pa)
	require.NoError(t, err)
	var names []string
	for _, f := range files {
		names = append(names, f.path)
	}
	assert.Equal(t, []string{"lib/util.buzz", entryFile}, names)
}

func TestPackRefuses(t *testing.T) {
	t.Parallel()
	empty := t.TempDir()
	_, err := Pack(t.Context(), empty, allTracked{})
	require.ErrorContains(t, err, "holds no tracked spell.buzz")

	_, err = Pack(t.Context(), spellDir(t), trackedOnly{"lib/util.buzz"})
	require.ErrorContains(t, err, "holds no tracked spell.buzz", "an untracked entry file is absent")

	linked := spellDir(t)
	require.NoError(t, os.Symlink(entryFile, filepath.Join(linked, "alias")))
	_, err = Pack(t.Context(), linked, allTracked{})
	require.ErrorContains(t, err, "is not a regular file")
}

// Only tracked files reach the layer, so an untracked dotfile on one machine does not
// change the digest a publish from another machine prints.
func TestPackSkipsUntrackedFiles(t *testing.T) {
	t.Parallel()
	tracked := trackedOnly{entryFile, "lib/util.buzz"}
	clean, err := Pack(t.Context(), spellDir(t), tracked)
	require.NoError(t, err)

	dirty := spellDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dirty, ".DS_Store"), []byte("finder"), 0o644))
	require.NoError(t, os.Symlink(entryFile, filepath.Join(dirty, "untracked-link")))
	got, err := Pack(t.Context(), dirty, tracked)
	require.NoError(t, err)
	assert.Equal(t, clean, got)
}

func TestResolvePullsOnceThenServesOffline(t *testing.T) {
	reg := newRegistry(t)
	src := spellDir(t)
	ref := publish(t, reg, src)
	root := t.TempDir()
	opts := Options{CacheRoot: root, Client: reg.srv.Client()}

	dir, err := Resolve(t.Context(), ref, opts)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "sha256-"+ref.OCI.Digest.Encoded(), "src"), dir)
	want, err := dirFiles(src)
	require.NoError(t, err)
	got, err := dirFiles(dir)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, 1, reg.pullCount())

	reg.srv.Close()
	t.Setenv("MAGUS_OFFLINE", "1")
	verified.Clear()
	again, err := Resolve(t.Context(), ref, opts)
	require.NoError(t, err)
	assert.Equal(t, dir, again)
	assert.Equal(t, 1, reg.pullCount(), "a verified cache entry needs no registry")

	// A cached copy that no longer verifies is a coded error offline, never a silent
	// pass. Clearing the memo stands in for a fresh process.
	require.NoError(t, os.WriteFile(filepath.Join(dir, entryFile), []byte("tampered\n"), 0o644))
	verified.Clear()
	_, err = Resolve(t.Context(), ref, opts)
	require.ErrorIs(t, err, types.RemoteSpellDigestMismatch)
}

func TestResolveOfflineWithoutCacheFails(t *testing.T) {
	t.Setenv("MAGUS_OFFLINE", "1")
	ref, err := Pinned("ghcr.io/team/spells/x", digest.FromBytes([]byte("m")))
	require.NoError(t, err)
	_, err = Resolve(t.Context(), ref, Options{CacheRoot: t.TempDir()})
	require.ErrorContains(t, err, "is not cached and MAGUS_OFFLINE is set")
}

func TestResolveRefusesAnotherManifest(t *testing.T) {
	reg := newRegistry(t)
	ref := publish(t, reg, spellDir(t))
	reg.mu.Lock()
	reg.manifests[ref.OCI.Digest.String()] = []byte(`{"schemaVersion":2}`)
	reg.mu.Unlock()

	root := t.TempDir()
	_, err := Resolve(t.Context(), ref, Options{CacheRoot: root, Client: reg.srv.Client()})
	require.ErrorIs(t, err, types.RemoteSpellDigestMismatch)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing is cached")
}

func TestResolveRefusesATamperedLayer(t *testing.T) {
	reg := newRegistry(t)
	src := spellDir(t)
	ref := publish(t, reg, src)
	layer, err := Pack(t.Context(), src, allTracked{})
	require.NoError(t, err)
	tampered := append([]byte{}, layer...)
	tampered[0] ^= 0xff
	reg.mu.Lock()
	reg.blobs[digest.FromBytes(layer).String()] = tampered
	reg.mu.Unlock()

	_, err = Resolve(t.Context(), ref, Options{CacheRoot: t.TempDir(), Client: reg.srv.Client()})
	require.ErrorIs(t, err, types.RemoteSpellDigestMismatch)
}

func TestResolveReplacesATamperedCache(t *testing.T) {
	reg := newRegistry(t)
	ref := publish(t, reg, spellDir(t))
	opts := Options{CacheRoot: t.TempDir(), Client: reg.srv.Client()}
	dir, err := Resolve(t.Context(), ref, opts)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, entryFile), []byte("tampered\n"), 0o644))
	again, err := Resolve(t.Context(), ref, opts)
	require.NoError(t, err)
	assert.Equal(t, dir, again)
	assert.Equal(t, 1, reg.pullCount(), "one process verifies a digest once")

	verified.Clear()
	again, err = Resolve(t.Context(), ref, opts)
	require.NoError(t, err)
	assert.Equal(t, dir, again)
	assert.Equal(t, 2, reg.pullCount(), "an entry that fails verification is pulled again")
	body, err := os.ReadFile(filepath.Join(dir, entryFile))
	require.NoError(t, err)
	assert.NotEqual(t, "tampered\n", string(body))
}

// A layer whose entries could extract to a tree other than the one it lists would
// fail cache verification on every resolve, or write outside the cache.
func TestWalkTarRefusesAmbiguousEntries(t *testing.T) {
	t.Parallel()
	layer := func(names ...string) []byte {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, n := range names {
			require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: n, Mode: 0o644, Size: 1}))
			_, err := tw.Write([]byte("x"))
			require.NoError(t, err)
		}
		require.NoError(t, tw.Close())
		return buf.Bytes()
	}
	for name, names := range map[string][]string{
		"escape":    {"../x"},
		"absolute":  {"/etc/x"},
		"unclean":   {"lib/../spell.buzz"},
		"dot":       {"./spell.buzz"},
		"backslash": {`lib\x`},
		"duplicate": {"spell.buzz", "spell.buzz"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := tarFiles(layer(names...))
			assert.Error(t, err)
		})
	}
	files, err := tarFiles(layer("spell.buzz", "lib/x"))
	require.NoError(t, err)
	x := sha256.Sum256([]byte("x"))
	assert.Equal(t, []fileSum{{path: "lib/x", sum: x}, {path: "spell.buzz", sum: x}}, files)
}

func TestResolveRefusesAnotherArtifactType(t *testing.T) {
	reg := newRegistry(t)
	dest := oci.Reference{Registry: reg.host(), Repository: "team/graph", Tag: "v1"}
	d, err := (&oci.Client{HTTP: reg.srv.Client()}).Push(t.Context(), dest, oci.Content{
		ArtifactType: "application/vnd.someone-else.v1",
		Layers:       []oci.Layer{{Name: layerTitle, MediaType: layerMediaType, Payload: []byte("x")}},
	})
	require.NoError(t, err)
	ref, err := Pinned(reg.host()+"/team/graph", d)
	require.NoError(t, err)
	_, err = Resolve(t.Context(), ref, Options{CacheRoot: t.TempDir(), Client: reg.srv.Client()})
	require.ErrorContains(t, err, "carries artifactType")
}

// Two builds of one commit, from checkouts whose files differ in mtime and mode and
// at different wall-clock moments, produce one manifest digest: created is the commit
// time, not the time of the build.
func TestBuildOfOneCommitHasOneDigest(t *testing.T) {
	commit := withRemote{
		fixedCommit: fixedCommit{ID: "0123abcd", Date: time.Date(2026, 9, 1, 12, 30, 0, 0, time.FixedZone("EDT", -4*3600))},
		remote:      "git@github.com:owner/repo.git",
	}
	build := func(dir string) ([]byte, digest.Digest) {
		prov, err := ReadProvenance(t.Context(), commit, dir)
		require.NoError(t, err)
		content, err := Build(t.Context(), dir, allTracked{}, prov)
		require.NoError(t, err)
		raw, d, err := content.Manifest()
		require.NoError(t, err)
		return raw, d
	}
	parent := t.TempDir()
	a, b := filepath.Join(parent, "a", "cursor"), filepath.Join(parent, "b", "cursor")
	for _, dir := range []string{a, b} {
		require.NoError(t, os.CopyFS(dir, os.DirFS(spellDir(t))))
	}
	old := time.Unix(1_000_000, 0)
	require.NoError(t, os.Chtimes(filepath.Join(b, entryFile), old, old))

	rawA, dA := build(a)
	_, dB := build(b)
	assert.Equal(t, dA, dB)

	var m ocispec.Manifest
	require.NoError(t, json.Unmarshal(rawA, &m))
	assert.Equal(t, map[string]string{
		ocispec.AnnotationTitle:    "cursor",
		ocispec.AnnotationSource:   "https://github.com/owner/repo",
		ocispec.AnnotationRevision: "0123abcd",
		ocispec.AnnotationCreated:  "2026-09-01T16:30:00Z",
	}, m.Annotations)
}

func TestReadProvenanceHonorsSourceDateEpoch(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	prov, err := ReadProvenance(t.Context(), fixedCommit{ID: "abc", Date: time.Unix(1, 0)}, "/src/spells/x")
	require.NoError(t, err)
	assert.Equal(t, Provenance{Title: "x", Revision: "abc", Created: time.Unix(1_700_000_000, 0)}, prov,
		"no remote capability means no source annotation")

	t.Setenv("SOURCE_DATE_EPOCH", "yesterday")
	_, err = ReadProvenance(t.Context(), fixedCommit{ID: "abc"}, "/src/spells/x")
	require.ErrorContains(t, err, "SOURCE_DATE_EPOCH")
}

// brokenRemote is a checkout whose remote could not be read at all.
type brokenRemote struct{ fixedCommit }

func (brokenRemote) RemoteURL(context.Context, string, string) (string, error) {
	return "", errors.New("hg paths default: signal: killed")
}

// Only "no remote configured" leaves the source empty. A remote that could not be read is
// an error, or the published provenance silently loses its source link.
func TestReadProvenanceSurfacesAFailedRemoteRead(t *testing.T) {
	_, err := ReadProvenance(t.Context(), brokenRemote{fixedCommit{ID: "abc"}}, "/src/spells/x")
	require.ErrorContains(t, err, "signal: killed")
}

func TestSourceURL(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"git@github.com:owner/repo.git":                  "https://github.com/owner/repo",
		"ssh://git@github.com/owner/repo.git":            "https://github.com/owner/repo",
		"https://github.com/owner/repo":                  "https://github.com/owner/repo",
		"https://x-access-token:ghs_abc@github.com/o/r":  "https://github.com/o/r",
		"https://gitlab.example:8443/group/sub/proj.git": "https://gitlab.example:8443/group/sub/proj",
		"ssh://git@gitlab.example:2222/group/proj.git":   "https://gitlab.example/group/proj",
		"/srv/git/repo.git":                              "",
		"file:///srv/git/repo.git":                       "",
		"":                                               "",
	} {
		assert.Equal(t, want, SourceURL(in), in)
	}
}

// A tag resolves to the digest the push printed, and pull-to-directory writes exactly
// the published files.
func TestPinAndUnpackATag(t *testing.T) {
	reg := newRegistry(t)
	src := spellDir(t)
	ref := publish(t, reg, src)
	c := &oci.Client{HTTP: reg.srv.Client()}

	pinned, err := Pin(t.Context(), c, oci.Reference{Registry: reg.host(), Repository: "team/spells/x", Tag: "v1"})
	require.NoError(t, err)
	assert.Equal(t, oci.Reference{Registry: reg.host(), Repository: "team/spells/x", Tag: "v1", Digest: ref.OCI.Digest}, pinned)

	cached, err := Resolve(t.Context(), Ref{Import: pinned.String(), OCI: pinned}, Options{CacheRoot: t.TempDir(), Client: reg.srv.Client()})
	require.NoError(t, err)
	dst := filepath.Join(t.TempDir(), "out")
	require.NoError(t, Unpack(cached, dst))
	want, err := dirFiles(src)
	require.NoError(t, err)
	got, err := dirFiles(dst)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	require.ErrorContains(t, Unpack(cached, dst), "not empty")
}

func TestResolveAuthenticatesAPrivatePull(t *testing.T) {
	reg := newRegistry(t)
	reg.mu.Lock()
	reg.user, reg.pass = "bot", "s3cret-token"
	reg.mu.Unlock()
	ref := publish(t, reg, spellDir(t))

	_, err := Resolve(t.Context(), ref, Options{CacheRoot: t.TempDir(), Client: reg.srv.Client()})
	require.ErrorContains(t, err, "401", "an anonymous pull of a private repository is refused")

	_, err = Resolve(t.Context(), ref, Options{CacheRoot: t.TempDir(), Client: reg.srv.Client(), Username: "bot", Password: "s3cret-token"})
	require.NoError(t, err)
}
