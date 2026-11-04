package oci

import (
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestParseDigestTakesSHA256Only(t *testing.T) {
	_, err := ParseDigest("sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	for _, s := range []string{"", "latest", "0123", "sha512:" + "00"} {
		_, err := ParseDigest(s)
		require.Error(t, err, s)
	}
}

func TestVerifyBlobReportsADigestMismatchAndALength(t *testing.T) {
	desc := ocispec.Descriptor{Digest: digest.FromString("a"), Size: 1}
	require.ErrorIs(t, VerifyBlob(t.Context(), strings.NewReader("b"), desc), ErrBlobDigest)
	require.ErrorContains(t, VerifyBlob(t.Context(), strings.NewReader("aa"), desc), "size is 2 bytes, descriptor says 1")
	require.NoError(t, VerifyBlob(t.Context(), strings.NewReader("a"), desc))
}
