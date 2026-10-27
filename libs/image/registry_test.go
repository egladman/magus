package image

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/oci"
)

// registry is an in-memory OCI distribution registry for one repository, enough of the
// v2 API for Pull and Push: blobs by digest, manifests by digest or tag.
type registry struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	blobs     map[digest.Digest][]byte
	manifests map[string]stored // by digest string and by tag
	uploads   int
}

type stored struct {
	mediaType string
	body      []byte
}

func newRegistry(t *testing.T) *registry {
	r := &registry{t: t, blobs: map[digest.Digest][]byte{}, manifests: map[string]stored{}}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *registry) client() *oci.Client { return &oci.Client{HTTP: r.srv.Client()} }

func (r *registry) uploadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.uploads
}

func (r *registry) repository() oci.Reference {
	return oci.Reference{Registry: strings.TrimPrefix(r.srv.URL, "https://"), Repository: "team/app"}
}

func (r *registry) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	path := req.URL.Path
	if path == "/v2/" {
		return
	}
	rest, ok := strings.CutPrefix(path, "/v2/team/app/")
	if !ok {
		http.NotFound(w, req)
		return
	}
	switch {
	case rest == "blobs/uploads/" && req.Method == http.MethodPost:
		w.Header().Set("Location", "/v2/team/app/blobs/uploads/one")
		w.WriteHeader(http.StatusAccepted)
	case strings.HasPrefix(rest, "blobs/uploads/") && req.Method == http.MethodPut:
		body, _ := io.ReadAll(req.Body)
		d := digest.Digest(req.URL.Query().Get("digest"))
		if digest.FromBytes(body) != d {
			http.Error(w, "digest mismatch", http.StatusBadRequest)
			return
		}
		r.blobs[d] = body
		r.uploads++
		w.WriteHeader(http.StatusCreated)
	case strings.HasPrefix(rest, "blobs/"):
		body, ok := r.blobs[digest.Digest(strings.TrimPrefix(rest, "blobs/"))]
		if !ok {
			http.NotFound(w, req)
			return
		}
		if req.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	case strings.HasPrefix(rest, "manifests/"):
		ref := strings.TrimPrefix(rest, "manifests/")
		if req.Method == http.MethodPut {
			body, _ := io.ReadAll(req.Body)
			m := stored{mediaType: req.Header.Get("Content-Type"), body: body}
			d := digest.FromBytes(body)
			r.manifests[d.String()], r.manifests[ref] = m, m
			w.Header().Set("Docker-Content-Digest", d.String())
			w.WriteHeader(http.StatusCreated)
			return
		}
		m, ok := r.manifests[ref]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", m.mediaType)
		_, _ = w.Write(m.body)
	default:
		http.NotFound(w, req)
	}
}

// put stores a blob or, with a manifest media type, a manifest, and returns its
// descriptor.
func (r *registry) put(mediaType string, body []byte, tags ...string) ocispec.Descriptor {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := ocispec.Descriptor{MediaType: mediaType, Digest: digest.FromBytes(body), Size: int64(len(body))}
	switch mediaType {
	case ocispec.MediaTypeImageManifest, ocispec.MediaTypeImageIndex, "application/vnd.docker.distribution.manifest.v2+json":
		r.manifests[d.Digest.String()] = stored{mediaType, body}
		for _, t := range tags {
			r.manifests[t] = stored{mediaType, body}
		}
	default:
		r.blobs[d.Digest] = body
	}
	return d
}

func (r *registry) putJSON(mediaType string, v any, tags ...string) ocispec.Descriptor {
	data, err := json.Marshal(v)
	require.NoError(r.t, err)
	return r.put(mediaType, data, tags...)
}

// publish stores a one-layer image for p and returns its manifest descriptor.
func (r *registry) publish(p Platform, content string) ocispec.Descriptor {
	layer := r.put(ocispec.MediaTypeImageLayerGzip, tarArchive(r.t, map[string]string{"etc/os-release": content}))
	cfg := r.putJSON(ocispec.MediaTypeImageConfig, ocispec.Image{
		Platform: ocispec.Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant},
		RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: []digest.Digest{layer.Digest}},
		Config:   ocispec.ImageConfig{User: "65532", Cmd: []string{"/bin/base"}},
	})
	return r.putJSON(ocispec.MediaTypeImageManifest, ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    cfg,
		Layers:    []ocispec.Descriptor{layer},
	})
}

func pinned(repo oci.Reference, d digest.Digest) oci.Reference {
	repo.Digest = d
	return repo
}

func TestPullMatchesAnArm64IndexEntryListedAsV8(t *testing.T) {
	reg := newRegistry(t)
	amd := reg.publish(Platform{OS: "linux", Architecture: "amd64"}, "amd")
	arm := reg.publish(Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}, "arm")
	amd.Platform = &ocispec.Platform{OS: "linux", Architecture: "amd64"}
	arm.Platform = &ocispec.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}
	idx := reg.putJSON(ocispec.MediaTypeImageIndex, ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{amd, arm},
	})

	dest := filepath.Join(t.TempDir(), "base")
	got, err := Pull(t.Context(), pinned(reg.repository(), idx.Digest), dest, PullOptions{
		Client: reg.client(), Platform: Platform{OS: "linux", Architecture: "arm64"},
	})
	require.NoError(t, err)
	assert.Equal(t, arm.Digest, got.Descriptor.Digest)
	assert.Equal(t, "v8", got.Platform.Variant)
	assert.Equal(t, pinned(reg.repository(), idx.Digest).String(), got.Descriptor.Annotations[ocispec.AnnotationRefName])
}

func TestPullRefusesADockerManifestAndLeavesDestAlone(t *testing.T) {
	reg := newRegistry(t)
	d := reg.put("application/vnd.docker.distribution.manifest.v2+json",
		[]byte(`{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}`))
	dest := filepath.Join(t.TempDir(), "base")
	require.NoError(t, os.MkdirAll(dest, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "keep"), []byte("x"), 0o644))

	_, err := Pull(t.Context(), pinned(reg.repository(), d.Digest), dest, PullOptions{
		Client: reg.client(), Platform: Platform{OS: "linux", Architecture: "amd64"},
	})
	require.ErrorContains(t, err, "only OCI image manifests and indexes")
	assert.FileExists(t, filepath.Join(dest, "keep"), "a refused pull leaves dest as it was")
}

func TestPullRefusesATagUnlessAllowed(t *testing.T) {
	reg := newRegistry(t)
	m := reg.publish(Platform{OS: "linux", Architecture: "amd64"}, "x")
	reg.put(m.MediaType, mustManifest(t, reg, m), "v1")
	ref := reg.repository()
	ref.Tag = "v1"
	opts := PullOptions{Client: reg.client(), Platform: Platform{OS: "linux", Architecture: "amd64"}}

	_, err := Pull(t.Context(), ref, filepath.Join(t.TempDir(), "a"), opts)
	require.ErrorContains(t, err, "names a tag")

	opts.AllowTag = true
	got, err := Pull(t.Context(), ref, filepath.Join(t.TempDir(), "b"), opts)
	require.NoError(t, err)
	assert.Equal(t, m.Digest, got.Descriptor.Digest)
}

func mustManifest(t *testing.T, reg *registry, d ocispec.Descriptor) []byte {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	m, ok := reg.manifests[d.Digest.String()]
	require.True(t, ok)
	return m.body
}

func TestPushThenPullRoundTripsAnAttachedIndex(t *testing.T) {
	reg := newRegistry(t)
	root := t.TempDir()
	idx := assembleIndex(t, root)
	sbom, err := WriteSBOM(t.Context(), idx, filepath.Join(root, "sbom.spdx.json"))
	require.NoError(t, err)
	attached, err := AttachReferrers(t.Context(), idx, []Document{sbom}, filepath.Join(root, "attached"))
	require.NoError(t, err)

	t.Setenv("MAGUS_OFFLINE", "0")
	pushed, err := Push(t.Context(), attached.Path, reg.repository(), PushOptions{Client: reg.client(), Tags: []string{"v1"}})
	require.NoError(t, err, "MAGUS_OFFLINE=0 means online, as libs/oci reads it")
	assert.Equal(t, attached.Descriptor.Digest, pushed.Digest)
	assert.Equal(t, pinned(reg.repository(), pushed.Digest), pushed.Reference())

	for _, p := range []Platform{{OS: "linux", Architecture: "amd64"}, {OS: "linux", Architecture: "arm64"}} {
		_, err := Pull(t.Context(), pinned(reg.repository(), pushed.Digest), filepath.Join(root, "pulled-"+p.Architecture),
			PullOptions{Client: reg.client(), Platform: p})
		require.NoError(t, err, "every platform manifest the index names reached the registry")
	}
	reg.mu.Lock()
	_, ok := reg.manifests[attached.Referrers[0].Digest.String()]
	reg.mu.Unlock()
	assert.True(t, ok, "the referrer manifest is pushed")
}

func TestPushSkipsBlobsTheRegistryHolds(t *testing.T) {
	reg := newRegistry(t)
	root := t.TempDir()
	l := assembleOne(t, root, Platform{OS: "linux", Architecture: "amd64"})
	_, err := Push(t.Context(), l, reg.repository(), PushOptions{Client: reg.client()})
	require.NoError(t, err)
	first := reg.uploadCount()
	assert.Positive(t, first)

	_, err = Push(t.Context(), l, reg.repository(), PushOptions{Client: reg.client()})
	require.NoError(t, err)
	assert.Equal(t, first, reg.uploadCount(), "a second push uploads nothing")
}

func TestPushRefusesAnOfflineClient(t *testing.T) {
	reg := newRegistry(t)
	l := assembleOne(t, t.TempDir(), Platform{OS: "linux", Architecture: "amd64"})
	t.Setenv("MAGUS_OFFLINE", "1")
	_, err := Push(context.Background(), l, reg.repository(), PushOptions{Client: reg.client()})
	require.True(t, errors.Is(err, oci.ErrOffline), "%v", err)
}

func tarArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var entries []entry
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, strings.ReplaceAll(name, "/", "_"))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		entries = append(entries, entry{path: "/" + name, kind: kindFile, source: p, mode: ModeFile})
	}
	var buf bytes.Buffer
	_, err := writeLayer(t.Context(), &buf, entries, maxLayerBytes)
	require.NoError(t, err)
	return buf.Bytes()
}

func assembleOne(t *testing.T, root string, p Platform) string {
	t.Helper()
	bin := write(t, filepath.Join(root, "bin-"+p.Architecture), "app "+p.String(), 0o755)
	dest := filepath.Join(root, "image-"+p.Architecture)
	_, err := Assemble(t.Context(), Spec{
		Scratch: true, Platform: p,
		Files:  []File{{Source: bin, Path: "/app", Mode: ModeExecutable}},
		Config: Config{Entrypoint: &[]string{"/app"}},
	}, dest)
	require.NoError(t, err)
	return dest
}

func assembleIndex(t *testing.T, root string) string {
	t.Helper()
	a := assembleOne(t, root, Platform{OS: "linux", Architecture: "amd64"})
	b := assembleOne(t, root, Platform{OS: "linux", Architecture: "arm64"})
	dest := filepath.Join(root, "index")
	_, err := AssembleIndex(t.Context(), []string{b, a}, dest)
	require.NoError(t, err, fmt.Sprintf("index over %s and %s", a, b))
	return dest
}
