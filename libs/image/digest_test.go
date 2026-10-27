package image

import (
	"strings"
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDigestRequiresSHA256(t *testing.T) {
	hex := digest.FromString("layer").Encoded()
	d, err := ParseDigest("sha256:" + hex)
	require.NoError(t, err)
	assert.Equal(t, "sha256", d.Algorithm())
	assert.Equal(t, hex, d.Encoded())
	assert.Equal(t, "sha256:"+hex, d.String())

	_, err = ParseDigest("")
	assert.ErrorContains(t, err, "image.digest: empty")
	_, err = ParseDigest("sha512:" + strings.Repeat("ab", 64))
	assert.ErrorContains(t, err, "want sha256")
	_, err = ParseDigest(hex)
	assert.Error(t, err)
}

func TestDiffIDDistinctFromLayerBlobDigest(t *testing.T) {
	blob := MustDigest(digest.FromString("gzipped"))
	diff := MustDiffID(digest.FromString("tar"))
	assert.NotEqual(t, blob.String(), diff.String())
	assert.False(t, blob.IsZero())
	assert.False(t, diff.IsZero())
}
