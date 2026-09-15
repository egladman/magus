package oci

import (
	"net/http"
	"os"
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

	for _, bad := range []string{
		"ghcr.io/egladman/magus",      // no tag
		"ghcr.io/egladman/magus:",     // empty tag
		"egladman/magus:v1",           // no registry host
		"localhost/egladman/magus:v1", // host with no dot reads as a repository segment
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
