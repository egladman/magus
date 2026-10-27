package image

import (
	"bytes"
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
	case ocispec.MediaTypeImageManifest, ocispec.MediaTypeImageIndex, mediaTypeDockerManifest, mediaTypeDockerManifestList:
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
