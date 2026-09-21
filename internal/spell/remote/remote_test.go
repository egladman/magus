package remote

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	case p == "/token":
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

// publish pushes a spell directory and returns the pinned import for it.
func publish(t *testing.T, reg *registry, dir string) string {
	t.Helper()
	dest := oci.Reference{Registry: reg.host(), Repository: "team/spells/x", Tag: "v1"}
	d, err := Publish(t.Context(), &oci.Client{HTTP: reg.srv.Client()}, dest, dir)
	require.NoError(t, err)
	return Scheme + reg.host() + "/team/spells/x@" + d.String()
}

func TestParse(t *testing.T) {
	t.Parallel()
	d := digest.FromBytes([]byte("m"))
	raw := Scheme + "ghcr.io/egladman/magus/spells/cursor@" + d.String()
	got, err := Parse(raw)
	require.NoError(t, err)
	assert.Equal(t, Ref{Import: raw, OCI: oci.Reference{Registry: "ghcr.io", Repository: "egladman/magus/spells/cursor", Digest: d}}, got)

	_, err = Parse(Scheme + "ghcr.io/egladman/magus/spells/cursor:latest")
	require.ErrorIs(t, err, types.RemoteSpellUnpinned, "a tag alone pins nothing")

	_, err = Parse(Scheme + "ghcr.io/egladman/magus/spells/cursor@sha256:abc")
	require.Error(t, err)
	assert.NotErrorIs(t, err, types.RemoteSpellUnpinned, "a malformed digest is a typo, not a missing pin")

	assert.True(t, IsRef(raw))
	assert.False(t, IsRef("spells/harness/cursor"))
	assert.False(t, IsRef("https://example.com/x"))
}

func TestPackIsDeterministic(t *testing.T) {
	t.Parallel()
	a, b := spellDir(t), spellDir(t)
	old := time.Unix(1_000_000, 0)
	require.NoError(t, os.Chtimes(filepath.Join(b, entryFile), old, old))
	require.NoError(t, os.Chmod(filepath.Join(b, "lib", "util.buzz"), 0o600))
	pa, err := Pack(a)
	require.NoError(t, err)
	pb, err := Pack(b)
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
	_, err := Pack(empty)
	require.ErrorContains(t, err, "holds no spell.buzz")

	linked := spellDir(t)
	require.NoError(t, os.Symlink(entryFile, filepath.Join(linked, "alias")))
	_, err = Pack(linked)
	require.ErrorContains(t, err, "is not a regular file")
}

func TestResolvePullsOnceThenServesOffline(t *testing.T) {
	reg := newRegistry(t)
	src := spellDir(t)
	ref, err := Parse(publish(t, reg, src))
	require.NoError(t, err)
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
	again, err := Resolve(t.Context(), ref, opts)
	require.NoError(t, err)
	assert.Equal(t, dir, again)
	assert.Equal(t, 1, reg.pullCount(), "a verified cache entry needs no registry")

	// A cached copy that no longer verifies is an error offline, never a silent pass.
	require.NoError(t, os.WriteFile(filepath.Join(dir, entryFile), []byte("tampered\n"), 0o644))
	_, err = Resolve(t.Context(), ref, opts)
	require.ErrorContains(t, err, "cached copy does not verify and MAGUS_OFFLINE is set")
}

func TestResolveOfflineWithoutCacheFails(t *testing.T) {
	t.Setenv("MAGUS_OFFLINE", "1")
	ref, err := Parse(Scheme + "ghcr.io/team/spells/x@" + digest.FromBytes([]byte("m")).String())
	require.NoError(t, err)
	_, err = Resolve(t.Context(), ref, Options{CacheRoot: t.TempDir()})
	require.ErrorContains(t, err, "is not cached and MAGUS_OFFLINE is set")
}

func TestResolveRefusesAnotherManifest(t *testing.T) {
	reg := newRegistry(t)
	ref, err := Parse(publish(t, reg, spellDir(t)))
	require.NoError(t, err)
	reg.mu.Lock()
	reg.manifests[ref.OCI.Digest.String()] = []byte(`{"schemaVersion":2}`)
	reg.mu.Unlock()

	root := t.TempDir()
	_, err = Resolve(t.Context(), ref, Options{CacheRoot: root, Client: reg.srv.Client()})
	require.ErrorIs(t, err, types.RemoteSpellDigestMismatch)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing is cached")
}

func TestResolveRefusesATamperedLayer(t *testing.T) {
	reg := newRegistry(t)
	src := spellDir(t)
	ref, err := Parse(publish(t, reg, src))
	require.NoError(t, err)
	layer, err := Pack(src)
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
	ref, err := Parse(publish(t, reg, spellDir(t)))
	require.NoError(t, err)
	opts := Options{CacheRoot: t.TempDir(), Client: reg.srv.Client()}
	dir, err := Resolve(t.Context(), ref, opts)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, entryFile), []byte("tampered\n"), 0o644))
	again, err := Resolve(t.Context(), ref, opts)
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
	d, err := (&oci.Client{HTTP: reg.srv.Client()}).Push(t.Context(), dest, "application/vnd.someone-else.v1",
		oci.Layer{Name: layerTitle, MediaType: layerMediaType, Payload: []byte("x")})
	require.NoError(t, err)
	ref, err := Parse(Scheme + reg.host() + "/team/graph@" + d.String())
	require.NoError(t, err)
	_, err = Resolve(t.Context(), ref, Options{CacheRoot: t.TempDir(), Client: reg.srv.Client()})
	require.ErrorContains(t, err, "carries artifactType")
}
