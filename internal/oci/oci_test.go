package oci

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPullAnonymouslyFromGHCR is the one test that proves the anonymous path against a
// REAL registry, which is the half of this package a unit test cannot speak to: whether
// ghcr.io hands a token to a caller with no credentials, and whether the blob GET
// survives the redirect to wherever it actually stores bytes.
//
// It is opt-in. `MAGUS_OCI_LIVE=1` runs it; every other run skips, so the gate never
// depends on a third party being up or on this machine having a network.
func TestPullAnonymouslyFromGHCR(t *testing.T) {
	if os.Getenv("MAGUS_OCI_LIVE") == "" {
		t.Skip("set MAGUS_OCI_LIVE=1 to reach ghcr.io")
	}
	c := &Client{HTTP: &http.Client{Timeout: 30 * time.Second}}

	ref, err := ParseReference("ghcr.io/homebrew/core/git:latest")
	require.NoError(t, err)

	// No credentials are set on the client on purpose: an unauthenticated reader is the
	// only case that matters for a published graph, and it is the case a token-less
	// client gets 401 on.
	tok, err := c.token(t.Context(), ref, "pull")
	require.NoError(t, err, "ghcr.io must issue an anonymous pull token for a public repository")
	assert.NotEmpty(t, tok)
}

// TestParseReference pins the shapes, including the two this deliberately refuses: a
// missing tag (a publish that retagged latest by accident is not undoable) and a bare
// repository with no registry host.
func TestParseReference(t *testing.T) {
	ref, err := ParseReference("ghcr.io/egladman/magus/knowledge-graph:v1")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io", ref.Registry)
	assert.Equal(t, "egladman/magus/knowledge-graph", ref.Repository)
	assert.Equal(t, "v1", ref.Tag)
	assert.Equal(t, "ghcr.io/egladman/magus/knowledge-graph:v1", ref.String())

	// localhost is a registry host without a dot, the one exception every container
	// runtime makes, so a local registry is reachable by the name people actually type.
	local, err := ParseReference("localhost:5000/egladman/magus:v1")
	require.NoError(t, err)
	assert.Equal(t, "localhost:5000", local.Registry)
	assert.Equal(t, "v1", local.Tag)

	for _, bad := range []string{
		"ghcr.io/egladman/magus",  // no tag
		"ghcr.io/egladman/magus:", // empty tag
		"egladman/magus:v1",       // no registry host
		"egladman/magus/graph:v1", // still no host, however many segments follow
		"",
	} {
		_, err := ParseReference(bad)
		assert.Error(t, err, "%q must not parse", bad)
	}
}

// TestEmptyConfigPayloadMatchesTheSpecDescriptor checks the two bytes this package
// uploads against the descriptor the spec fixes for them. The descriptor is the spec's
// own value, so a mismatch means the PAYLOAD drifted, and every manifest written after
// that would name a config blob no registry holds.
func TestEmptyConfigPayloadMatchesTheSpecDescriptor(t *testing.T) {
	assert.Equal(t, ocispec.DescriptorEmptyJSON.Digest, digest.FromBytes(emptyConfigPayload))
	assert.Equal(t, ocispec.DescriptorEmptyJSON.Size, int64(len(emptyConfigPayload)))
	assert.Equal(t, ocispec.MediaTypeEmptyJSON, ocispec.DescriptorEmptyJSON.MediaType)
}

// fakeRegistry is enough of the v2 API to push and pull: a token endpoint, the two-step
// blob upload, and tag- or digest-addressed manifests. It serves TLS because the client addresses
// every registry as https, and httptest's own client trusts its certificate.
type fakeRegistry struct {
	srv   *httptest.Server
	mu    sync.Mutex
	blobs map[string][]byte
	tags  map[string][]byte
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	r := &fakeRegistry{blobs: map[string][]byte{}, tags: map[string][]byte{}}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *fakeRegistry) client() *Client { return &Client{HTTP: r.srv.Client()} }

func (r *fakeRegistry) ref(repo, tag string) Reference {
	return Reference{Registry: strings.TrimPrefix(r.srv.URL, "https://"), Repository: repo, Tag: tag}
}

func (r *fakeRegistry) serve(w http.ResponseWriter, req *http.Request) {
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
	// Before the /blobs/ case, which its path also matches.
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
			r.tags[last] = body
			// A registry also serves a manifest by its digest.
			r.tags[digest.FromBytes(body).String()] = body
			w.WriteHeader(http.StatusCreated)
			return
		}
		b, ok := r.tags[last]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(b)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

const testArtifactType = "application/vnd.magus.test.v1+json"

// TestPushAndFetchLayersByTitle is the whole point of the multi-layer shape: a reader
// names one layer and gets that layer's bytes, without reading the others.
func TestPushAndFetchLayersByTitle(t *testing.T) {
	reg := newFakeRegistry(t)
	c := reg.client()
	ref := reg.ref("team/graph", "latest")

	index := []byte(`{"schema_version":1}`)
	shard := []byte(`{"name":"docs","nodes":[]}`)
	pushed, err := c.Push(t.Context(), ref, testArtifactType,
		Layer{Name: "manifest.json", MediaType: "application/json", Payload: index},
		Layer{Name: "fp-docs", MediaType: "application/json", Payload: shard},
	)
	require.NoError(t, err)

	art, err := c.Artifact(t.Context(), ref, testArtifactType)
	require.NoError(t, err)
	assert.Equal(t, pushed, digest.FromBytes(art.Raw), "Push returns the digest of the manifest a pull reads")

	// The same manifest, addressed by the digest Push returned.
	pinned := ref
	pinned.Tag, pinned.Digest = "", pushed
	art, err = c.Artifact(t.Context(), pinned, testArtifactType)
	require.NoError(t, err)
	require.Len(t, art.Manifest.Layers, 2)
	assert.Equal(t, "manifest.json", art.Manifest.Layers[0].Annotations[ocispec.AnnotationTitle],
		"the title annotation is what a puller addresses a layer by")

	got, err := art.Layer(t.Context(), "fp-docs")
	require.NoError(t, err)
	assert.Equal(t, shard, got)

	_, err = art.Layer(t.Context(), "fp-absent")
	assert.ErrorIs(t, err, ErrLayerMiss, "an absent title is a miss, distinguishable from a transport failure")
}

// TestArtifactRefusesAWrongArtifactType pins the check that stops a tag someone else's
// tooling wrote to from yielding bytes that merely parse as JSON.
func TestArtifactRefusesAWrongArtifactType(t *testing.T) {
	reg := newFakeRegistry(t)
	c := reg.client()
	ref := reg.ref("team/graph", "latest")
	_, err := c.Push(t.Context(), ref, testArtifactType,
		Layer{Name: "only", MediaType: "application/json", Payload: []byte(`{}`)})
	require.NoError(t, err)

	_, err = c.Artifact(t.Context(), ref, "application/vnd.someone-else.v1+json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), testArtifactType)
}

// TestBlobRefusesContentTheManifestDoesNotDescribe covers the two bounds a layer fetch
// carries: the digest the manifest named, and the size it declared.
func TestBlobRefusesContentTheManifestDoesNotDescribe(t *testing.T) {
	reg := newFakeRegistry(t)
	c := reg.client()
	ref := reg.ref("team/graph", "latest")
	payload := []byte(`{"nodes":[]}`)
	_, err := c.Push(t.Context(), ref, testArtifactType,
		Layer{Name: "shard", MediaType: "application/json", Payload: payload})
	require.NoError(t, err)

	art, err := c.Artifact(t.Context(), ref, testArtifactType)
	require.NoError(t, err)

	reg.mu.Lock()
	reg.blobs[digest.FromBytes(payload).String()] = []byte(`{"nodes":{}}`) // same LENGTH, different bytes
	reg.mu.Unlock()

	_, err = art.Layer(t.Context(), "shard")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match the digest",
		"same-length corruption must be caught by the digest, since the size bound cannot see it")

	reg.mu.Lock()
	reg.blobs[digest.FromBytes(payload).String()] = append(payload, ' ')
	reg.mu.Unlock()

	_, err = art.Layer(t.Context(), "shard")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manifest declared",
		"an overlong body must be named as a size mismatch, not blamed on the digest")
}

// TestPushRefusesAnEmptyArtifact keeps a manifest with no layers off a registry: it is
// not a valid artifact, and the failure it causes surfaces on the puller's machine.
func TestPushRefusesAnEmptyArtifact(t *testing.T) {
	reg := newFakeRegistry(t)
	_, err := reg.client().Push(t.Context(), reg.ref("team/graph", "latest"), testArtifactType)
	assert.Error(t, err)
}

// TestPinnedPullRefusesAnotherManifest pins the check that makes a digest reference mean
// something: the registry answering with a different manifest is refused before decode.
func TestPinnedPullRefusesAnotherManifest(t *testing.T) {
	reg := newFakeRegistry(t)
	c := reg.client()
	ref := reg.ref("team/graph", "latest")
	pushed, err := c.Push(t.Context(), ref, testArtifactType,
		Layer{Name: "only", MediaType: "application/json", Payload: []byte(`{}`)})
	require.NoError(t, err)

	reg.mu.Lock()
	reg.tags[pushed.String()] = []byte(`{"schemaVersion":2}`)
	reg.mu.Unlock()
	pinned := ref
	pinned.Tag, pinned.Digest = "", pushed
	_, err = c.Artifact(t.Context(), pinned, "")
	require.ErrorIs(t, err, ErrManifestDigest)
}

func TestParseReferenceDigest(t *testing.T) {
	d := digest.FromBytes([]byte("m"))
	ref, err := ParseReference("ghcr.io/egladman/magus/spells/cursor@" + d.String())
	require.NoError(t, err)
	assert.Equal(t, Reference{Registry: "ghcr.io", Repository: "egladman/magus/spells/cursor", Digest: d}, ref)
	assert.Equal(t, "ghcr.io/egladman/magus/spells/cursor@"+d.String(), ref.String())

	both, err := ParseReference("localhost:5000/team/spell:v1@" + d.String())
	require.NoError(t, err)
	assert.Equal(t, Reference{Registry: "localhost:5000", Repository: "team/spell", Tag: "v1", Digest: d}, both)

	_, err = ParseReference("ghcr.io/team/spell@sha256:abc")
	assert.Error(t, err, "a truncated digest names nothing")

	_, err = ParseReference("ghcr.io/team/spell:@" + d.String())
	assert.Error(t, err, "an empty tag is a typo, not an absent one")
}

func TestPushRefusesADigestReference(t *testing.T) {
	reg := newFakeRegistry(t)
	ref := reg.ref("team/graph", "latest")
	ref.Digest = digest.FromBytes([]byte("m"))
	_, err := reg.client().Push(t.Context(), ref, testArtifactType,
		Layer{Name: "only", MediaType: "application/json", Payload: []byte(`{}`)})
	assert.ErrorContains(t, err, "name a tag and no digest")
}
