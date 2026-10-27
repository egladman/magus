package oci

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// docker.io answers /v2/ with a redirect to www.docker.com; only registry-1.docker.io
// serves the API, and an official image lives under library/.
func TestDockerHubRequestsGoToRegistry1AndABareNameIsRefused(t *testing.T) {
	for _, host := range []string{"docker.io", "index.docker.io"} {
		ref, err := ParseReference(host + "/library/alpine:3")
		require.NoError(t, err)
		assert.Equal(t, host+"/library/alpine:3", ref.String(), "the reference keeps what was written")
		assert.Equal(t, "https://registry-1.docker.io/v2/library/alpine", (&Client{}).base(ref))

		_, err = ParseReference(host + "/alpine:3")
		require.ErrorContains(t, err, host+"/library/alpine")
	}
	ghcr, err := ParseReference("ghcr.io/alpine:3")
	require.NoError(t, err, "a one-segment repository is only a Docker Hub mistake")
	assert.Equal(t, "https://ghcr.io/v2/alpine", (&Client{}).base(ghcr))
}

func TestReferrersTagIsAlgDashEncoded(t *testing.T) {
	d := digest.FromString("subject")
	assert.Equal(t, "sha256-"+d.Encoded(), ReferrersTag(d))
	require.NoError(t, ValidateTag(ReferrersTag(d)))
}

func referrerBody(t *testing.T, subject RawManifest, artifactType string, annotations map[string]string) []byte {
	t.Helper()
	body, err := json.Marshal(ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: artifactType,
		Config:       ocispec.DescriptorEmptyJSON,
		Layers:       []ocispec.Descriptor{ocispec.DescriptorEmptyJSON},
		Subject:      &subject.Descriptor,
		Annotations:  annotations,
	})
	require.NoError(t, err)
	return body
}

// distribution-spec "Pushing Manifests with Subject": without OCI-Subject in the answer
// the client MUST add the referrer to the <alg>-<ref> tag index, and a reader that gets a
// 404 from the Referrers API MUST read that tag.
func TestPushManifestKeepsTheReferrersTagWhenTheRegistryHasNoReferrersAPI(t *testing.T) {
	reg := newInMemoryRegistry(t)
	c := reg.client()
	subject := raw(ocispec.MediaTypeImageIndex, indexBody(t))
	_, err := c.PushManifest(t.Context(), reg.repository(), subject)
	require.NoError(t, err)

	sbom := raw(ocispec.MediaTypeImageManifest, referrerBody(t, subject, "application/spdx+json", map[string]string{"org.opencontainers.image.created": "1970-01-01T00:00:00Z"}))
	for range 2 {
		_, err = c.PushManifest(t.Context(), reg.repository(), sbom)
		require.NoError(t, err)
	}

	reg.mu.Lock()
	tagged := reg.tags[ReferrersTag(subject.Digest)]
	reg.mu.Unlock()
	var idx ocispec.Index
	require.NoError(t, json.Unmarshal(tagged, &idx))
	require.Len(t, idx.Manifests, 1, "a second push of the same referrer adds nothing")
	got := idx.Manifests[0]
	assert.Equal(t, sbom.Digest, got.Digest)
	assert.Equal(t, "application/spdx+json", got.ArtifactType)
	assert.Equal(t, "1970-01-01T00:00:00Z", got.Annotations["org.opencontainers.image.created"])

	pinned := reg.pinned(subject.Digest)
	found, err := c.Referrers(t.Context(), pinned, "")
	require.NoError(t, err)
	assert.Equal(t, []digest.Digest{sbom.Digest}, digests(found))
	none, err := c.Referrers(t.Context(), pinned, "application/vnd.in-toto+json")
	require.NoError(t, err)
	assert.Empty(t, none, "the artifactType filter applies to the fallback tag too")
}

func TestPushManifestLeavesTheReferrersTagAloneWhenTheRegistryProcessesTheSubject(t *testing.T) {
	reg := newInMemoryRegistry(t)
	reg.referrersAPI = true
	c := reg.client()
	subject := raw(ocispec.MediaTypeImageIndex, indexBody(t))
	_, err := c.PushManifest(t.Context(), reg.repository(), subject)
	require.NoError(t, err)
	sbom := raw(ocispec.MediaTypeImageManifest, referrerBody(t, subject, "application/spdx+json", nil))
	_, err = c.PushManifest(t.Context(), reg.repository(), sbom)
	require.NoError(t, err)

	reg.mu.Lock()
	_, tagged := reg.tags[ReferrersTag(subject.Digest)]
	reg.mu.Unlock()
	assert.False(t, tagged)

	found, err := c.Referrers(t.Context(), reg.pinned(subject.Digest), "application/spdx+json")
	require.NoError(t, err)
	assert.Equal(t, []digest.Digest{sbom.Digest}, digests(found))
}

func TestReferrersOfANeverReferencedSubjectIsEmpty(t *testing.T) {
	reg := newInMemoryRegistry(t)
	found, err := reg.client().Referrers(t.Context(), reg.pinned(digest.FromString("alone")), "")
	require.NoError(t, err)
	assert.Empty(t, found)
	_, err = reg.client().Referrers(t.Context(), reg.repository(), "")
	require.ErrorContains(t, err, "name the subject by digest")
}

func digests(ds []ocispec.Descriptor) []digest.Digest {
	out := make([]digest.Digest, len(ds))
	for i, d := range ds {
		out[i] = d.Digest
	}
	return out
}

// distribution-spec "Error Codes": a 4XX body is {"errors":[{code,message,detail}]}.
func TestStatusErrorReadsTheSpecErrorBody(t *testing.T) {
	reg := newInMemoryRegistry(t)
	reg.intercept = func(w http.ResponseWriter, req *http.Request) bool {
		if !strings.Contains(req.URL.Path, "/blobs/") {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[{"code":"BLOB_UNKNOWN","message":"blob unknown to registry","detail":{"digest":"x"}}]}`))
		return true
	}
	err := reg.client().GetBlob(t.Context(), reg.repository(), blobDescriptor([]byte("absent")), new(bytes.Buffer))
	require.ErrorIs(t, err, ErrNotFound)
	var status *StatusError
	require.ErrorAs(t, err, &status)
	require.Len(t, status.Errors, 1)
	assert.Equal(t, "BLOB_UNKNOWN", status.Errors[0].Code)
	assert.JSONEq(t, `{"digest":"x"}`, string(status.Errors[0].Detail))
	assert.Empty(t, status.Body)
	assert.ErrorContains(t, err, "BLOB_UNKNOWN: blob unknown to registry")
}

func TestForbiddenIsDeniedNotUnauthorized(t *testing.T) {
	reg := newInMemoryRegistry(t)
	reg.intercept = func(w http.ResponseWriter, req *http.Request) bool {
		if !strings.Contains(req.URL.Path, "/blobs/") {
			return false
		}
		w.WriteHeader(http.StatusForbidden)
		return true
	}
	err := reg.client().GetBlob(t.Context(), reg.repository(), blobDescriptor([]byte("x")), new(bytes.Buffer))
	require.ErrorIs(t, err, ErrDenied)
	assert.False(t, errors.Is(err, ErrUnauthorized))
}

func TestTooManyRequestsWaitsOutAShortRetryAfter(t *testing.T) {
	reg := newInMemoryRegistry(t)
	payload := []byte("rate limited")
	reg.blobs[blobDescriptor(payload).Digest.String()] = payload
	limited := 0
	reg.intercept = func(w http.ResponseWriter, req *http.Request) bool {
		if !strings.Contains(req.URL.Path, "/blobs/") || limited == 2 {
			return false
		}
		limited++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		return true
	}
	var got bytes.Buffer
	require.NoError(t, reg.client().GetBlob(t.Context(), reg.repository(), blobDescriptor(payload), &got))
	assert.Equal(t, payload, got.Bytes())
	assert.Equal(t, 2, limited)
}

func TestTooManyRequestsWithALongRetryAfterFailsAtOnce(t *testing.T) {
	reg := newInMemoryRegistry(t)
	reg.intercept = func(w http.ResponseWriter, req *http.Request) bool {
		if !strings.Contains(req.URL.Path, "/blobs/") {
			return false
		}
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"errors":[{"code":"TOOMANYREQUESTS","message":"pull rate limit"}]}`))
		return true
	}
	err := reg.client().GetBlob(t.Context(), reg.repository(), blobDescriptor([]byte("x")), new(bytes.Buffer))
	require.ErrorIs(t, err, ErrTooManyRequests)
	assert.ErrorContains(t, err, "TOOMANYREQUESTS: pull rate limit; retry after 3600")
}

// distribution-spec "Warnings": clients SHOULD report `Warning: 299 - "..."` headers.
func TestRegistryWarningIsLogged(t *testing.T) {
	logged := &capturedLog{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	reg := newInMemoryRegistry(t)
	payload := []byte("warned")
	reg.blobs[blobDescriptor(payload).Digest.String()] = payload
	reg.intercept = func(w http.ResponseWriter, req *http.Request) bool {
		w.Header().Add("Warning", `299 - "this repository is deprecated"`)
		w.Header().Add("Warning", `199 - "not a registry warning"`)
		return false
	}
	require.NoError(t, reg.client().GetBlob(t.Context(), reg.repository(), blobDescriptor(payload), new(bytes.Buffer)))

	logged.mu.Lock()
	defer logged.mu.Unlock()
	assert.Contains(t, logged.buf.String(), `"warning":"this repository is deprecated"`)
	assert.NotContains(t, logged.buf.String(), "not a registry warning")
}
