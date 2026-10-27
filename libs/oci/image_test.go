package oci

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/egladman/magus/internal/secret"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const imageTestPassword = "hunter2-plaintext-password"

func blobDescriptor(payload []byte) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromBytes(payload),
		Size:      int64(len(payload)),
	}
}

func (r *inMemoryRegistry) repository() Reference {
	return r.ref("team/image", "")
}

func (r *inMemoryRegistry) pinned(d digest.Digest) Reference {
	ref := r.ref("team/image", "")
	ref.Digest = d
	return ref
}

func indexBody(t *testing.T) []byte {
	t.Helper()
	return []byte(`{"schemaVersion":2,"mediaType":"` + ocispec.MediaTypeImageIndex + `","manifests":[]}`)
}

// requestCount reads the registry's request counter under its lock.
func (r *inMemoryRegistry) requestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests
}

func TestImageManifestFetchesAnIndexByDigest(t *testing.T) {
	reg := newInMemoryRegistry(t)
	body := indexBody(t)
	want, err := reg.client().PutManifest(t.Context(), reg.repository(), ocispec.MediaTypeImageIndex, body, "v1")
	require.NoError(t, err)

	got, err := reg.client().Manifest(t.Context(), reg.pinned(want))
	require.NoError(t, err)

	assert.Equal(t, RawManifest{
		Descriptor: ocispec.Descriptor{MediaType: ocispec.MediaTypeImageIndex, Digest: digest.FromBytes(body), Size: int64(len(body))},
		Body:       body,
	}, got)
	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Equal(t, []string{strings.Join([]string{
		ocispec.MediaTypeImageManifest, ocispec.MediaTypeImageIndex, mediaTypeDockerManifest, mediaTypeDockerManifestList,
	}, ", ")}, reg.accept)
}

func TestImageManifestFetchesADockerManifest(t *testing.T) {
	reg := newInMemoryRegistry(t)
	body := []byte(`{"schemaVersion":2,"mediaType":"` + mediaTypeDockerManifest + `"}`)
	d, err := reg.client().PutManifest(t.Context(), reg.repository(), mediaTypeDockerManifest, body)
	require.NoError(t, err)

	got, err := reg.client().Manifest(t.Context(), reg.pinned(d))
	require.NoError(t, err)
	assert.Equal(t, ocispec.Descriptor{MediaType: mediaTypeDockerManifest, Digest: d, Size: int64(len(body))}, got.Descriptor)
}

func TestImageManifestRefusesATag(t *testing.T) {
	reg := newInMemoryRegistry(t)
	_, err := reg.client().Manifest(t.Context(), reg.ref("team/image", "v1"))
	require.ErrorContains(t, err, "name a digest")
	assert.Zero(t, reg.requestCount(), "a tag is refused before any request")
}

func TestImageManifestRefusesBytesThatMissTheDigest(t *testing.T) {
	reg := newInMemoryRegistry(t)
	body := indexBody(t)
	d, err := reg.client().PutManifest(t.Context(), reg.repository(), ocispec.MediaTypeImageIndex, body)
	require.NoError(t, err)
	reg.mu.Lock()
	reg.tags[d.String()] = []byte(`{"schemaVersion":2,"manifests":[{}]}`)
	reg.mu.Unlock()

	_, err = reg.client().Manifest(t.Context(), reg.pinned(d))
	require.ErrorIs(t, err, ErrManifestDigest)
}

func TestImageManifestRefusesAContentTypeThatDisagreesWithTheBody(t *testing.T) {
	reg := newInMemoryRegistry(t)
	body := indexBody(t)
	d := digest.FromBytes(body)
	reg.mu.Lock()
	reg.tags[d.String()] = body
	reg.types[d.String()] = ocispec.MediaTypeImageManifest
	reg.mu.Unlock()

	_, err := reg.client().Manifest(t.Context(), reg.pinned(d))
	require.ErrorContains(t, err, "disagrees with the manifest's mediaType")
}

func TestImageManifestRefusesAnUnknownMediaType(t *testing.T) {
	reg := newInMemoryRegistry(t)
	body := []byte(`{"schemaVersion":2}`)
	d := digest.FromBytes(body)
	reg.mu.Lock()
	reg.tags[d.String()] = body
	reg.types[d.String()] = "text/html"
	reg.mu.Unlock()

	_, err := reg.client().Manifest(t.Context(), reg.pinned(d))
	require.ErrorContains(t, err, `media type "text/html" is not a manifest or index`)
}

func TestImagePushBlobThenFetchBlobStreamsTheBytes(t *testing.T) {
	reg := newInMemoryRegistry(t)
	payload := bytes.Repeat([]byte("layer-bytes-"), 1000)
	desc := blobDescriptor(payload)

	require.NoError(t, reg.client().PushBlob(t.Context(), reg.repository(), desc, bytes.NewReader(payload)))
	var got bytes.Buffer
	require.NoError(t, reg.client().FetchBlob(t.Context(), reg.repository(), desc, &got))

	assert.Equal(t, payload, got.Bytes())
	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Equal(t, map[string][]byte{desc.Digest.String(): payload}, reg.blobs)
}

func TestImagePushBlobSkipsABlobTheRegistryHolds(t *testing.T) {
	reg := newInMemoryRegistry(t)
	payload := []byte("already there")
	desc := blobDescriptor(payload)
	require.NoError(t, reg.client().PushBlob(t.Context(), reg.repository(), desc, bytes.NewReader(payload)))

	read := false
	require.NoError(t, reg.client().PushBlob(t.Context(), reg.repository(), desc, readFunc(func([]byte) (int, error) {
		read = true
		return 0, errors.New("must not be read")
	})))

	assert.False(t, read, "the source of a present blob is never read")
	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Equal(t, 1, reg.uploads)
}

type readFunc func([]byte) (int, error)

func (f readFunc) Read(p []byte) (int, error) { return f(p) }

func TestImagePushBlobRefusesBytesThatMissTheDescriptor(t *testing.T) {
	good := []byte("the declared bytes")
	cases := map[string]struct {
		desc   ocispec.Descriptor
		source []byte
		want   string
		stored map[string][]byte
	}{
		"wrong digest": {blobDescriptor(good), []byte("the declared byteZ"), "does not match the digest", map[string][]byte{}},
		"short":        {blobDescriptor(good), good[:5], "5 bytes, 18 declared", map[string][]byte{}},
		"long":         {blobDescriptor(good), append(bytes.Clone(good), 'x'), "19 bytes, 18 declared", map[string][]byte{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			reg := newInMemoryRegistry(t)
			err := reg.client().PushBlob(t.Context(), reg.repository(), tc.desc, bytes.NewReader(tc.source))
			require.ErrorContains(t, err, tc.want)
			reg.mu.Lock()
			defer reg.mu.Unlock()
			assert.Equal(t, tc.stored, reg.blobs)
		})
	}
}

func TestImagePushBlobChecksASizedSourceBeforeAnyRequest(t *testing.T) {
	good := []byte("the declared bytes")
	reg := newInMemoryRegistry(t)
	err := reg.client().PushBlob(t.Context(), reg.repository(), blobDescriptor(good), bytes.NewReader(good[:5]))
	require.ErrorContains(t, err, "5 bytes, 18 declared")
	assert.Zero(t, reg.requestCount())
}

// A reader that cannot say its length is checked as it streams. Whatever the registry
// stored must be the blob the descriptor names, never the overlong source.
func TestImagePushBlobRefusesAnUnsizedSourceThatRunsLong(t *testing.T) {
	good := []byte("the declared bytes")
	reg := newInMemoryRegistry(t)
	err := reg.client().PushBlob(t.Context(), reg.repository(), blobDescriptor(good), io.MultiReader(bytes.NewReader(append(bytes.Clone(good), 'x'))))
	require.ErrorContains(t, err, "longer than the 18 bytes declared")
	reg.mu.Lock()
	defer reg.mu.Unlock()
	for d, b := range reg.blobs {
		assert.Equal(t, blobDescriptor(good).Digest.String(), d)
		assert.Equal(t, good, b)
	}
}

func TestImagePushBlobRefusesAnUnprotectedUploadLocation(t *testing.T) {
	reg := newInMemoryRegistry(t)
	reg.mu.Lock()
	reg.uploadLocation = "http://uploads.example/upload/1"
	reg.mu.Unlock()
	payload := []byte("payload")

	err := reg.client().PushBlob(t.Context(), reg.repository(), blobDescriptor(payload), bytes.NewReader(payload))

	require.ErrorContains(t, err, "named a http upload location")
	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Empty(t, reg.blobs)
}

func TestImageFetchBlobRefusesBytesThatMissTheDescriptor(t *testing.T) {
	payload := []byte("registry blob content")
	for name, tc := range map[string]struct {
		served []byte
		size   int64
		want   string
	}{
		"wrong digest": {[]byte("registry blob contenT"), int64(len(payload)), "does not match the digest"},
		"short":        {payload[:4], int64(len(payload)), "4 bytes, 21 declared"},
		"long":         {append(bytes.Clone(payload), "more"...), int64(len(payload)), "longer than the 21 bytes declared"},
		"size lies":    {payload, int64(len(payload)) + 3, "21 bytes, 24 declared"},
	} {
		t.Run(name, func(t *testing.T) {
			reg := newInMemoryRegistry(t)
			desc := blobDescriptor(payload)
			desc.Size = tc.size
			reg.mu.Lock()
			reg.blobs[desc.Digest.String()] = tc.served
			reg.mu.Unlock()

			err := reg.client().FetchBlob(t.Context(), reg.repository(), desc, new(bytes.Buffer))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestImageFetchBlobFollowsARedirectWithoutCredentialsAcrossHosts(t *testing.T) {
	reg := newInMemoryRegistry(t)
	payload := []byte("served from blob storage")
	desc := blobDescriptor(payload)
	var mu sync.Mutex
	var storageSaw []http.Header
	storage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		storageSaw = append(storageSaw, req.Header.Clone())
		mu.Unlock()
		_, _ = w.Write(payload)
	}))
	t.Cleanup(storage.Close)
	reg.mu.Lock()
	reg.blobs[desc.Digest.String()] = payload
	reg.blobRedirect = storage.URL
	reg.enforceAuth = true
	reg.mu.Unlock()

	var got bytes.Buffer
	require.NoError(t, reg.client().FetchBlob(t.Context(), reg.repository(), desc, &got))

	assert.Equal(t, payload, got.Bytes())
	reg.mu.Lock()
	assert.Equal(t, []string{"Bearer issued-1"}, reg.blobAuth, "the registry itself saw the bearer token")
	reg.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, storageSaw, 1)
	assert.Empty(t, storageSaw[0].Values("Authorization"), "the token never reaches the storage host")
}

func TestImageFetchBlobRefusesARedirectToHTTP(t *testing.T) {
	reg := newInMemoryRegistry(t)
	payload := []byte("must not be fetched over http")
	desc := blobDescriptor(payload)
	var mu sync.Mutex
	var hits int
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		_, _ = w.Write(payload)
	}))
	t.Cleanup(plain.Close)
	reg.mu.Lock()
	reg.blobs[desc.Digest.String()] = payload
	reg.blobRedirect = plain.URL
	reg.mu.Unlock()

	var got bytes.Buffer
	err := reg.client().FetchBlob(t.Context(), reg.repository(), desc, &got)

	require.ErrorContains(t, err, "refused a redirect from https to "+plain.URL)
	assert.Empty(t, got.Bytes())
	mu.Lock()
	defer mu.Unlock()
	assert.Zero(t, hits, "the plain http server is never reached")
}

func TestImageErrorsAreBranchable(t *testing.T) {
	reg := newInMemoryRegistry(t)
	payload := []byte("absent")
	desc := blobDescriptor(payload)

	err := reg.client().FetchBlob(t.Context(), reg.repository(), desc, new(bytes.Buffer))
	require.ErrorIs(t, err, ErrNotFound)
	var status *StatusError
	require.ErrorAs(t, err, &status)
	assert.Equal(t, 404, status.Code)

	reg.mu.Lock()
	reg.enforceAuth, reg.rejectTokens = true, true
	reg.mu.Unlock()
	err = reg.client().PushBlob(t.Context(), reg.repository(), desc, bytes.NewReader(payload))
	require.ErrorIs(t, err, ErrUnauthorized)

	t.Setenv("MAGUS_OFFLINE", "1")
	require.ErrorIs(t, reg.client().FetchBlob(t.Context(), reg.repository(), desc, new(bytes.Buffer)), ErrOffline)
}

func TestImageResolveReadsATagAndReturnsItsDigest(t *testing.T) {
	reg := newInMemoryRegistry(t)
	body := indexBody(t)
	want, err := reg.client().PutManifest(t.Context(), reg.repository(), ocispec.MediaTypeImageIndex, body, "v1")
	require.NoError(t, err)

	got, err := reg.client().Resolve(t.Context(), reg.ref("team/image", "v1"))
	require.NoError(t, err)
	assert.Equal(t, RawManifest{Descriptor: ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageIndex,
		Digest:    want,
		Size:      int64(len(body)),
	}, Body: body}, got)
}

func TestReferenceTextRoundTrips(t *testing.T) {
	ref, err := ParseReference("localhost:5000/team/app:v1")
	require.NoError(t, err)
	text, err := ref.MarshalText()
	require.NoError(t, err)
	var back Reference
	require.NoError(t, back.UnmarshalText(text))
	assert.Equal(t, ref, back)
	_, err = Reference{}.MarshalText()
	require.Error(t, err)
}

func TestImagePutManifestWritesTheDigestThenEachTag(t *testing.T) {
	reg := newInMemoryRegistry(t)
	body := indexBody(t)
	want := digest.FromBytes(body)
	reg.mu.Lock()
	reg.dockerDigest = func(b []byte) string { return digest.FromBytes(b).String() }
	reg.mu.Unlock()

	got, err := reg.client().PutManifest(t.Context(), reg.repository(), ocispec.MediaTypeImageIndex, body, "v1", "latest", "v1")

	require.NoError(t, err)
	assert.Equal(t, want, got)
	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Equal(t, map[string][]byte{"v1": body, "latest": body, want.String(): body}, reg.tags)
	assert.Equal(t, map[string]string{
		"v1": ocispec.MediaTypeImageIndex, "latest": ocispec.MediaTypeImageIndex, want.String(): ocispec.MediaTypeImageIndex,
	}, reg.types)
}

func TestImagePutManifestWithoutTagsWritesTheDigestOnly(t *testing.T) {
	reg := newInMemoryRegistry(t)
	body := indexBody(t)

	got, err := reg.client().PutManifest(t.Context(), reg.repository(), ocispec.MediaTypeImageIndex, body)

	require.NoError(t, err)
	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Equal(t, map[string][]byte{got.String(): body}, reg.tags)
}

func TestImagePutManifestRefusesADigestTheRegistryDoesNotConfirm(t *testing.T) {
	reg := newInMemoryRegistry(t)
	body := indexBody(t)
	other := digest.FromString("something else")
	reg.mu.Lock()
	reg.dockerDigest = func([]byte) string { return other.String() }
	reg.mu.Unlock()

	_, err := reg.client().PutManifest(t.Context(), reg.repository(), ocispec.MediaTypeImageIndex, body, "v1")

	require.ErrorContains(t, err, "the registry confirmed "+other.String())
}

func TestImagePutManifestRefusesBadInputBeforeAnyRequest(t *testing.T) {
	reg := newInMemoryRegistry(t)
	body := indexBody(t)

	_, err := reg.client().PutManifest(t.Context(), reg.repository(), ocispec.MediaTypeImageIndex, body, "v1", "bad tag")
	require.ErrorContains(t, err, `tag "bad tag"`)
	_, err = reg.client().PutManifest(t.Context(), reg.repository(), "text/plain", body)
	require.ErrorContains(t, err, `media type "text/plain" is not a manifest or index`)

	assert.Zero(t, reg.requestCount())
}

func TestImageRequestsRefuseWhenOffline(t *testing.T) {
	reg := newInMemoryRegistry(t)
	t.Setenv("MAGUS_OFFLINE", "1")
	payload := []byte("offline")
	desc := blobDescriptor(payload)
	c := reg.client()

	_, manifestErr := c.Manifest(t.Context(), reg.pinned(desc.Digest))
	fetchErr := c.FetchBlob(t.Context(), reg.repository(), desc, new(bytes.Buffer))
	pushErr := c.PushBlob(t.Context(), reg.repository(), desc, bytes.NewReader(payload))
	_, putErr := c.PutManifest(t.Context(), reg.repository(), ocispec.MediaTypeImageIndex, indexBody(t), "v1")
	_, artifactErr := c.Artifact(t.Context(), reg.ref("team/image", "v1"), "")
	_, tagsErr := c.Tags(t.Context(), reg.ref("team/image", ""))
	_, legacyPushErr := c.Push(t.Context(), reg.ref("team/image", "v1"), testContent(Layer{Name: "only", MediaType: "application/json", Payload: []byte(`{}`)}))

	for name, err := range map[string]error{
		"Manifest": manifestErr, "FetchBlob": fetchErr, "PushBlob": pushErr,
		"PutManifest": putErr, "Artifact": artifactErr, "Tags": tagsErr, "Push": legacyPushErr,
	} {
		require.ErrorContains(t, err, "MAGUS_OFFLINE is set, so no request is sent", name)
	}
	assert.Zero(t, reg.requestCount(), "nothing reached the registry")
}

func TestImageExpiredTokenIsRefreshedOnce(t *testing.T) {
	reg := newInMemoryRegistry(t)
	payload := []byte("behind a token")
	desc := blobDescriptor(payload)
	reg.mu.Lock()
	reg.blobs[desc.Digest.String()] = payload
	reg.enforceAuth = true
	reg.mu.Unlock()
	c := reg.client()

	require.NoError(t, c.FetchBlob(t.Context(), reg.repository(), desc, new(bytes.Buffer)))
	reg.expire()
	var got bytes.Buffer
	require.NoError(t, c.FetchBlob(t.Context(), reg.repository(), desc, &got))

	assert.Equal(t, payload, got.Bytes())
	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Equal(t, 2, reg.issued, "one token per expiry, not one per request")
	assert.Equal(t, []string{"Bearer issued-1", "Bearer issued-2"}, reg.blobAuth,
		"the stale token was refused, and the retry carried a fresh one")
}

func TestImageRefusedTokenIsNotRetriedForever(t *testing.T) {
	reg := newInMemoryRegistry(t)
	desc := blobDescriptor([]byte("never served"))
	reg.mu.Lock()
	reg.enforceAuth, reg.rejectTokens = true, true
	reg.mu.Unlock()

	err := reg.client().FetchBlob(t.Context(), reg.repository(), desc, new(bytes.Buffer))

	require.ErrorContains(t, err, "401")
	reg.mu.Lock()
	defer reg.mu.Unlock()
	assert.Equal(t, 2, reg.issued, "the first token and exactly one refresh")
}

type capturedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *capturedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func TestImagePasswordNeverAppearsInAnErrorOrALog(t *testing.T) {
	logged := &capturedLog{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logged, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	creds := Credentials{Username: "bot", Password: secret.NewValue(imageTestPassword)}
	basicHeader := base64.StdEncoding.EncodeToString([]byte("bot:" + imageTestPassword))
	payload := []byte("private")
	desc := blobDescriptor(payload)

	var errs []string
	for _, basicOnly := range []bool{false, true} {
		reg := newInMemoryRegistry(t)
		reg.mu.Lock()
		reg.basicOnly = basicOnly
		reg.enforceAuth, reg.rejectTokens = true, true
		reg.mu.Unlock()
		c := reg.client()
		c.Credentials = creds
		_, err := c.Manifest(t.Context(), reg.pinned(desc.Digest))
		errs = append(errs, fmt.Sprint(err))
		errs = append(errs, fmt.Sprint(c.FetchBlob(t.Context(), reg.repository(), desc, new(bytes.Buffer))))
		errs = append(errs, fmt.Sprint(c.PushBlob(t.Context(), reg.repository(), desc, bytes.NewReader(payload[:2]))))
	}
	errs = append(errs, fmt.Sprintf("%v %+v %#v %q", creds, creds, creds, creds))

	for _, text := range append(errs, logged.buf.String()) {
		assert.NotContains(t, text, imageTestPassword)
		assert.NotContains(t, text, basicHeader)
	}
}

func TestImageDefaultClientGuardsRedirects(t *testing.T) {
	c := &Client{}
	require.NotNil(t, c.http().CheckRedirect)

	own := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	custom := &Client{HTTP: &http.Client{CheckRedirect: own}}
	assert.Equal(t, fmt.Sprintf("%p", own), fmt.Sprintf("%p", custom.http().CheckRedirect), "a caller's own policy is kept")
}
