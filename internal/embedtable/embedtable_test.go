package embedtable

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
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/embed"
	"github.com/egladman/magus/libs/oci"
)

// registry is enough of the distribution API for a push and a pull. Like GHCR, it
// answers tags/list for a repository never pushed to with NAME_UNKNOWN.
type registry struct {
	srv       *httptest.Server
	mu        sync.Mutex
	blobs     map[string][]byte
	manifests map[string][]byte
	uploads   int
	blobGets  int
}

func newRegistry(t *testing.T) *registry {
	t.Helper()
	r := &registry{blobs: map[string][]byte{}, manifests: map[string][]byte{}}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *registry) client() *oci.Client { return &oci.Client{HTTP: r.srv.Client()} }

func (r *registry) ref(tag string) oci.Reference {
	return oci.Reference{Registry: strings.TrimPrefix(r.srv.URL, "https://"), Repository: "egladman/magus-embed", Tag: tag}
}

func (r *registry) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := req.URL.Path
	last := p[strings.LastIndex(p, "/")+1:]
	switch {
	case p == "/v2/":
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="https://%s/token",service="%s"`, req.Host, req.Host))
		w.WriteHeader(http.StatusUnauthorized)
	case p == "/token":
		_, _ = w.Write([]byte(`{"token":"issued"}`))
	case strings.HasPrefix(p, "/upload/"):
		body, _ := io.ReadAll(req.Body)
		r.blobs[req.URL.Query().Get("digest")] = body
		w.WriteHeader(http.StatusCreated)
	case strings.HasSuffix(p, "/blobs/uploads/"):
		r.uploads++
		w.Header().Set("Location", "/upload/1")
		w.WriteHeader(http.StatusAccepted)
	case strings.HasSuffix(p, "/tags/list"):
		var tags []string
		for name := range r.manifests {
			if !strings.Contains(name, ":") {
				tags = append(tags, name)
			}
		}
		if len(tags) == 0 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[{"code":"NAME_UNKNOWN","message":"repository name not known to registry"}]}`))
			return
		}
		slices.Sort(tags)
		body, _ := json.Marshal(map[string]any{"tags": tags})
		_, _ = w.Write(body)
	case strings.Contains(p, "/blobs/"):
		b, ok := r.blobs[last]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if req.Method != http.MethodHead {
			r.blobGets++
			_, _ = w.Write(b)
		}
	case strings.Contains(p, "/manifests/"):
		if req.Method == http.MethodPut {
			body, _ := io.ReadAll(req.Body)
			r.manifests[last] = body
			r.manifests[digest.FromBytes(body).String()] = body
			w.WriteHeader(http.StatusCreated)
			return
		}
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

// testTable writes a two-token table to dir and returns its path, bytes and pin.
func testTable(t *testing.T, dir string) (string, []byte, string) {
	t.Helper()
	spec := embed.TableSpec{
		Tokens:  []string{"alpha", "beta"},
		Weights: []uint16{1, 2},
		Rows:    [][]int8{make([]int8, 64), make([]int8, 64)},
		Prov: embed.Provenance{
			Teacher: "minishlab/potion-code-16M-v2", TeacherRevision: "rev1", TextsDigest: "texts1",
			PairsDigest: "pairs1", Pipeline: "convert", Run: "run1", GOARCH: "arm64",
		},
	}
	spec.Rows[1][0] = 9
	var buf bytes.Buffer
	require.NoError(t, embed.EncodeTable(&buf, spec))
	path := filepath.Join(dir, fileName)
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))
	return path, buf.Bytes(), sum(buf.Bytes())
}

func TestEmbedTableReferenceIsTaggedWithThePin(t *testing.T) {
	pin, err := embed.Pin()
	require.NoError(t, err)
	ref, err := Reference("")
	require.NoError(t, err)
	assert.Equal(t, oci.Reference{Registry: "ghcr.io", Repository: "egladman/magus-embed", Tag: pin}, ref)

	_, err = Reference("ghcr.io/egladman/magus-embed:latest")
	assert.Error(t, err, "a repository carries no tag; the pin is the tag")
}

func TestEmbedTableLoadReportsErrAbsentWithTheCommand(t *testing.T) {
	_, err := load(Options{Dir: t.TempDir()})
	require.ErrorIs(t, err, errAbsent)
	assert.Contains(t, err.Error(), "magus graph pull -table")
}

func TestEmbedTableLoadRefusesBytesOtherThanThePin(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, fileName), []byte("not the table"), 0o644))
	_, err := load(Options{Dir: dir})
	require.Error(t, err)
	assert.NotErrorIs(t, err, errAbsent)
	assert.Contains(t, err.Error(), "the pin is")
}

func TestEmbedTablePublishThenPullThenLoad(t *testing.T) {
	r := newRegistry(t)
	src, _, pin := testTable(t, t.TempDir())
	ctx := context.Background()

	pushed, err := publish(ctx, r.client(), r.ref(pin), src, pin)
	require.NoError(t, err)
	assert.True(t, pushed)

	dst := filepath.Join(t.TempDir(), "magus-embed", fileName)
	require.NoError(t, pull(ctx, r.client(), r.ref(pin), dst, pin))
	tab, err := readPinned(dst, pin)
	require.NoError(t, err)
	assert.Equal(t, 2, tab.Len())
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(dst), fileName+".*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers, "the temporary file is renamed into place")
}

func TestEmbedTablePullRefusesALayerOtherThanThePin(t *testing.T) {
	r := newRegistry(t)
	_, _, pin := testTable(t, t.TempDir())
	ctx := context.Background()
	wrong := oci.Content{ArtifactType: artifactType, Layers: []oci.Layer{{Name: fileName, MediaType: layerMediaType, Payload: []byte("other bytes")}}}
	_, err := r.client().Push(ctx, r.ref(pin), wrong)
	require.NoError(t, err)

	dst := filepath.Join(t.TempDir(), fileName)
	err = pull(ctx, r.client(), r.ref(pin), dst, pin)
	require.ErrorContains(t, err, "the pin is sha256:"+pin)
	assert.Zero(t, r.blobGets, "the layer is refused before it is fetched")
	_, statErr := os.Stat(dst)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "the cache is left as it was")
}

func TestEmbedTablePublishIsWriteOnce(t *testing.T) {
	r := newRegistry(t)
	src, _, pin := testTable(t, t.TempDir())
	ctx := context.Background()

	pushed, err := publish(ctx, r.client(), r.ref(pin), src, pin)
	require.NoError(t, err)
	require.True(t, pushed)
	uploads := r.uploads

	pushed, err = publish(ctx, r.client(), r.ref(pin), src, pin)
	require.NoError(t, err, "a rerun keeps the tag that already holds the pin")
	assert.False(t, pushed)
	assert.Equal(t, uploads, r.uploads, "a kept tag uploads nothing")
}

func TestEmbedTablePublishRefusesATagHoldingAnotherLayer(t *testing.T) {
	r := newRegistry(t)
	src, _, pin := testTable(t, t.TempDir())
	ctx := context.Background()
	wrong := oci.Content{ArtifactType: artifactType, Layers: []oci.Layer{{Name: fileName, MediaType: layerMediaType, Payload: []byte("other bytes")}}}
	_, err := r.client().Push(ctx, r.ref(pin), wrong)
	require.NoError(t, err)
	uploads := r.uploads

	_, err = publish(ctx, r.client(), r.ref(pin), src, pin)
	require.ErrorContains(t, err, "never overwritten")
	assert.Equal(t, uploads, r.uploads)
}

func TestEmbedTablePublishRefusesAFileThatMissesThePin(t *testing.T) {
	r := newRegistry(t)
	src, _, pin := testTable(t, t.TempDir())
	ctx := context.Background()

	_, err := publish(ctx, r.client(), r.ref(pin), src, strings.Repeat("0", 64))
	require.ErrorContains(t, err, "the tag must be the pin")
	_, err = publish(ctx, r.client(), r.ref(strings.Repeat("0", 64)), src, strings.Repeat("0", 64))
	require.ErrorContains(t, err, "never published under another tag")
	assert.Zero(t, r.uploads)

	_, err = publish(ctx, r.client(), r.ref(pin), filepath.Join(t.TempDir(), fileName), pin)
	require.ErrorIs(t, err, errAbsent)
	assert.Contains(t, err.Error(), "magus run table libs/embed/train")
}
