package base64

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodingBase64RoundTrip(t *testing.T) {
	ctx := context.Background()
	const in = "magus extra: \x00\x01\xff bytes"
	enc, err := Base64Encode(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, "bWFndXMgZXh0cmE6IAAB/yBieXRlcw==", enc)
	back, err := Base64Decode(ctx, enc)
	require.NoError(t, err)
	assert.Equal(t, in, back)
}

func TestEncodingBase64URLDiffersFromStd(t *testing.T) {
	ctx := context.Background()
	// 0xFB,0xFF encodes to "+/" in std base64 and "-_" in URL-safe base64.
	in := string([]byte{0xfb, 0xff})
	std, _ := Base64Encode(ctx, in)
	url, _ := Base64URLEncode(ctx, in)
	assert.NotEqual(t, std, url, "expected std and url base64 to differ for %x", in)
	back, err := Base64URLDecode(ctx, url)
	require.NoError(t, err)
	assert.Equal(t, in, back)
}

func TestEncodingBase64DecodeErrors(t *testing.T) {
	ctx := context.Background()
	_, err := Base64Decode(ctx, "not base64!!!")
	assert.Error(t, err, "base64_decode of garbage should error")
}
