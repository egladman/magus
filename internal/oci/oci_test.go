package oci

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus/internal/json"
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
	auth, err := c.authorize(t.Context(), ref, "pull")
	require.NoError(t, err, "ghcr.io must issue an anonymous pull token for a public repository")
	assert.True(t, strings.HasPrefix(auth, "Bearer "), auth)
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
	// uploads counts blob upload sessions started.
	uploads int
	// tokenAuth records the Authorization header of each token request.
	tokenAuth []string
	// pings counts unauthenticated /v2/ requests.
	pings int
	// realm, when set, replaces the challenge's own /token realm; basicOnly answers
	// with a Basic challenge instead of a Bearer one.
	realm     string
	basicOnly bool
	// manifestAuth records the Authorization header of each manifest request.
	manifestAuth []string
	// pageSize, when set, paginates tags/list through a relative Link header.
	pageSize int
	// link, when set, replaces the Link header tags/list would send.
	link string
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
	case p == "/v2/":
		r.pings++
		realm := "https://" + req.Host + "/token"
		if r.realm != "" {
			realm = r.realm
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="`+realm+`",service="`+req.Host+`"`)
		if r.basicOnly {
			w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
		}
		w.WriteHeader(http.StatusUnauthorized)
	case p == "/token":
		r.tokenAuth = append(r.tokenAuth, req.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"token":"fake"}`))
	case strings.HasPrefix(p, "/upload/"):
		body, _ := io.ReadAll(req.Body)
		r.blobs[req.URL.Query().Get("digest")] = body
		w.WriteHeader(http.StatusCreated)
	// Before the /blobs/ case, which its path also matches.
	case strings.HasSuffix(p, "/blobs/uploads/"):
		r.uploads++
		w.Header().Set("Location", "/upload/1")
		w.WriteHeader(http.StatusAccepted)
	case strings.HasSuffix(p, "/tags/list"):
		r.serveTags(w, req)
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
		r.manifestAuth = append(r.manifestAuth, req.Header.Get("Authorization"))
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

// serveTags answers tags/list in lexical order, after ?last= and at most pageSize per
// page, the way the distribution spec paginates.
func (r *fakeRegistry) serveTags(w http.ResponseWriter, req *http.Request) {
	var all []string
	for name := range r.tags {
		if !strings.Contains(name, ":") {
			all = append(all, name)
		}
	}
	slices.Sort(all)
	if last := req.URL.Query().Get("last"); last != "" {
		all = all[slices.Index(all, last)+1:]
	}
	page := all
	if r.pageSize > 0 && len(all) > r.pageSize {
		page = all[:r.pageSize]
		w.Header().Set("Link", fmt.Sprintf(`<%s?n=%d&last=%s>; rel="next"`, req.URL.Path, r.pageSize, page[len(page)-1]))
	}
	if r.link != "" {
		w.Header().Set("Link", r.link)
	}
	body, _ := json.Marshal(map[string]any{"name": "team/graph", "tags": page})
	_, _ = w.Write(body)
}

const testArtifactType = "application/vnd.magus.test.v1+json"

func testContent(layers ...Layer) Content {
	return Content{ArtifactType: testArtifactType, Layers: layers}
}

// TestPushAndFetchLayersByTitle is the whole point of the multi-layer shape: a reader
// names one layer and gets that layer's bytes, without reading the others.
func TestPushAndFetchLayersByTitle(t *testing.T) {
	reg := newFakeRegistry(t)
	c := reg.client()
	ref := reg.ref("team/graph", "latest")

	index := []byte(`{"schema_version":1}`)
	shard := []byte(`{"name":"docs","nodes":[]}`)
	pushed, err := c.Push(t.Context(), ref, testContent(
		Layer{Name: "manifest.json", MediaType: "application/json", Payload: index},
		Layer{Name: "fp-docs", MediaType: "application/json", Payload: shard},
	))
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
	_, err := c.Push(t.Context(), ref, testContent(
		Layer{Name: "only", MediaType: "application/json", Payload: []byte(`{}`)}))
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
	_, err := c.Push(t.Context(), ref, testContent(
		Layer{Name: "shard", MediaType: "application/json", Payload: payload}))
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
	_, err := reg.client().Push(t.Context(), reg.ref("team/graph", "latest"), testContent())
	assert.Error(t, err)
}

// TestPinnedPullRefusesAnotherManifest pins the check that makes a digest reference mean
// something: the registry answering with a different manifest is refused before decode.
func TestPinnedPullRefusesAnotherManifest(t *testing.T) {
	reg := newFakeRegistry(t)
	c := reg.client()
	ref := reg.ref("team/graph", "latest")
	pushed, err := c.Push(t.Context(), ref, testContent(
		Layer{Name: "only", MediaType: "application/json", Payload: []byte(`{}`)}))
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
	_, err := reg.client().Push(t.Context(), ref, testContent(
		Layer{Name: "only", MediaType: "application/json", Payload: []byte(`{}`)}))
	assert.ErrorContains(t, err, "name a tag and no digest")
}

func TestParseRepository(t *testing.T) {
	ref, err := ParseRepository("localhost:5000/team/spells/x")
	require.NoError(t, err)
	assert.Equal(t, Reference{Registry: "localhost:5000", Repository: "team/spells/x"}, ref)

	for _, bad := range []string{
		"ghcr.io/team/x:v1",
		"ghcr.io/team/x@" + digest.FromBytes([]byte("m")).String(),
		"team/x",
	} {
		_, err := ParseRepository(bad)
		assert.Error(t, err, "%q must not parse as a repository", bad)
	}
}

// One upload per blob however many tags a push writes: each extra tag is a manifest PUT
// of the same bytes, so every tag serves the digest Push returned.
func TestPushWritesEveryTagFromOneUpload(t *testing.T) {
	reg := newFakeRegistry(t)
	c := reg.client()
	content := testContent(Layer{Name: "only", MediaType: "application/json", Payload: []byte(`{"a":1}`)})

	pushed, err := c.Push(t.Context(), reg.ref("team/graph", "v1"), content, "latest", "v1", "stable")
	require.NoError(t, err)
	raw, want, err := content.Manifest()
	require.NoError(t, err)
	assert.Equal(t, want, pushed, "Push returns the digest Manifest computes offline")

	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Equal(t, 2, reg.uploads, "the config blob and the one layer, once each")
	assert.Equal(t, map[string][]byte{"v1": raw, "latest": raw, "stable": raw, pushed.String(): raw}, reg.tags)
}

func TestPushValidatesEveryTagBeforeUploading(t *testing.T) {
	reg := newFakeRegistry(t)
	_, err := reg.client().Push(t.Context(), reg.ref("team/graph", "v1"),
		testContent(Layer{Name: "only", MediaType: "application/json", Payload: []byte(`{}`)}), "not/a/tag")
	require.ErrorContains(t, err, `tag "not/a/tag"`)
	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Equal(t, 0, reg.uploads)
	assert.Empty(t, reg.tags)
}

// Annotations land in the manifest itself, and key order cannot move the digest.
func TestContentManifestCarriesAnnotations(t *testing.T) {
	layer := Layer{Name: "only", MediaType: "application/json", Payload: []byte(`{}`)}
	a := Content{ArtifactType: testArtifactType, Layers: []Layer{layer},
		Annotations: map[string]string{ocispec.AnnotationRevision: "abc", ocispec.AnnotationCreated: "2026-01-02T03:04:05Z"}}
	b := a
	b.Annotations = map[string]string{ocispec.AnnotationCreated: "2026-01-02T03:04:05Z", ocispec.AnnotationRevision: "abc"}

	rawA, dA, err := a.Manifest()
	require.NoError(t, err)
	_, dB, err := b.Manifest()
	require.NoError(t, err)
	assert.Equal(t, dA, dB)

	var m ocispec.Manifest
	require.NoError(t, json.Unmarshal(rawA, &m))
	assert.Equal(t, a.Annotations, m.Annotations)

	_, _, err = Content{ArtifactType: testArtifactType}.Manifest()
	assert.ErrorContains(t, err, "no layers")
}

func TestTagsFollowsLinkPagination(t *testing.T) {
	reg := newFakeRegistry(t)
	c := reg.client()
	_, err := c.Push(t.Context(), reg.ref("team/graph", "v1"),
		testContent(Layer{Name: "only", MediaType: "application/json", Payload: []byte(`{}`)}), "v2", "v3", "latest", "v4")
	require.NoError(t, err)
	reg.mu.Lock()
	reg.pageSize = 2
	reg.mu.Unlock()

	tags, err := c.Tags(t.Context(), reg.ref("team/graph", ""))
	require.NoError(t, err)
	assert.Equal(t, []string{"latest", "v1", "v2", "v3", "v4"}, tags)
}

// The next page is fetched with this repository's bearer token, so a Link naming
// another host would hand the token to it.
func TestTagsRefusesACrossOriginNextPage(t *testing.T) {
	reg := newFakeRegistry(t)
	c := reg.client()
	reg.mu.Lock()
	reg.link = `<https://elsewhere.example/v2/team/graph/tags/list?last=v1>; rel="next"`
	reg.mu.Unlock()
	_, err := c.Tags(t.Context(), reg.ref("team/graph", ""))
	require.ErrorContains(t, err, "another origin")
}

// Credentials reach the token endpoint on a pull as well as a push, and an anonymous
// client sends none. One client asks once per scope: the Tags call reuses the pull token.
func TestTokenCarriesCredentialsOnPull(t *testing.T) {
	reg := newFakeRegistry(t)
	_, err := reg.client().Push(t.Context(), reg.ref("team/graph", "v1"),
		testContent(Layer{Name: "only", MediaType: "application/json", Payload: []byte(`{}`)}))
	require.NoError(t, err)

	authed := reg.client()
	authed.Username, authed.Password = "bot", "s3cret-token"
	_, err = authed.Artifact(t.Context(), reg.ref("team/graph", "v1"), testArtifactType)
	require.NoError(t, err)
	_, err = authed.Tags(t.Context(), reg.ref("team/graph", ""))
	require.NoError(t, err)

	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("bot:s3cret-token"))
	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Equal(t, []string{"", basic}, reg.tokenAuth)
	assert.Equal(t, 2, reg.pings, "one challenge per client")
}

func TestParseChallenge(t *testing.T) {
	scheme, params := parseChallenge(`Bearer realm="https://auth.example/token?a=1,b=2",service="reg.example", scope="repository:team/x:pull,push",note="say \"hi\""`)
	assert.Equal(t, "Bearer", scheme)
	assert.Equal(t, map[string]string{
		"realm":   "https://auth.example/token?a=1,b=2",
		"service": "reg.example",
		"scope":   "repository:team/x:pull,push",
		"note":    `say "hi"`,
	}, params)

	scheme, params = parseChallenge(`Basic realm=registry`)
	assert.Equal(t, "Basic", scheme)
	assert.Equal(t, map[string]string{"realm": "registry"}, params)
}

// The realm is wherever the challenge says, on another host here, and each token is
// scoped to the repository and the actions of the call that needed it.
func TestAuthorizeFollowsARealmOnAnotherHost(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	auth := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if req.URL.Path != "/auth/issue" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := req.URL.Query()
		asked = append(asked, q.Get("service")+" "+q.Get("scope"))
		_, _ = fmt.Fprintf(w, `{"access_token":"tok-%s"}`, q.Get("scope"))
	}))
	t.Cleanup(auth.Close)
	reg := newFakeRegistry(t)
	reg.mu.Lock()
	reg.realm = auth.URL + "/auth/issue"
	reg.mu.Unlock()
	c := reg.client()

	ref := reg.ref("team/x", "v1")
	_, err := c.Push(t.Context(), ref, testContent(Layer{Name: "only", MediaType: "application/json", Payload: []byte(`{}`)}))
	require.NoError(t, err)
	for range 2 {
		_, err = c.Artifact(t.Context(), ref, testArtifactType)
		require.NoError(t, err)
	}

	host := reg.ref("", "").Registry
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{host + " repository:team/x:push,pull", host + " repository:team/x:pull"}, asked,
		"one token per scope; the second pull reuses the first")
	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Equal(t, []string{
		"Bearer tok-repository:team/x:push,pull",
		"Bearer tok-repository:team/x:pull",
		"Bearer tok-repository:team/x:pull",
	}, reg.manifestAuth)
	assert.Empty(t, reg.tokenAuth, "the registry's own /token is never asked")
}

// A registry that answers /v2/ with only a Basic challenge gets the credentials on
// every request and no token exchange; without credentials it is refused up front.
func TestAuthorizeAnswersABasicChallenge(t *testing.T) {
	reg := newFakeRegistry(t)
	reg.mu.Lock()
	reg.basicOnly = true
	reg.mu.Unlock()
	c := reg.client()
	c.Username, c.Password = "bot", "s3cret-token"

	ref := reg.ref("team/x", "v1")
	_, err := c.Push(t.Context(), ref, testContent(Layer{Name: "only", MediaType: "application/json", Payload: []byte(`{}`)}))
	require.NoError(t, err)
	_, err = c.Artifact(t.Context(), ref, testArtifactType)
	require.NoError(t, err)

	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("bot:s3cret-token"))
	reg.mu.Lock()
	assert.Equal(t, []string{basic, basic}, reg.manifestAuth)
	assert.Empty(t, reg.tokenAuth)
	reg.mu.Unlock()

	_, err = reg.client().Artifact(t.Context(), ref, testArtifactType)
	require.ErrorContains(t, err, "asks for Basic credentials")
}
