package oci

import (
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
