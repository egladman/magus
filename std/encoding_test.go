package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodingBase64RoundTrip(t *testing.T) {
	ctx := context.Background()
	const in = "magus extra: \x00\x01\xff bytes"
	enc, err := EncodingBase64Encode(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, "bWFndXMgZXh0cmE6IAAB/yBieXRlcw==", enc)
	back, err := EncodingBase64Decode(ctx, enc)
	require.NoError(t, err)
	assert.Equal(t, in, back)
}

func TestEncodingBase64URLDiffersFromStd(t *testing.T) {
	ctx := context.Background()
	// 0xFB,0xFF encodes to "+/" in std base64 and "-_" in URL-safe base64.
	in := string([]byte{0xfb, 0xff})
	std, _ := EncodingBase64Encode(ctx, in)
	url, _ := EncodingBase64URLEncode(ctx, in)
	assert.NotEqual(t, std, url, "expected std and url base64 to differ for %x", in)
	back, err := EncodingBase64URLDecode(ctx, url)
	require.NoError(t, err)
	assert.Equal(t, in, back)
}

func TestEncodingHexRoundTrip(t *testing.T) {
	ctx := context.Background()
	enc, err := EncodingHexEncode(ctx, "abc")
	require.NoError(t, err)
	assert.Equal(t, "616263", enc)
	back, err := EncodingHexDecode(ctx, enc)
	require.NoError(t, err)
	assert.Equal(t, "abc", back)
}

func TestEncodingURLRoundTrip(t *testing.T) {
	ctx := context.Background()
	const in = "a b&c=d/e"
	enc, err := EncodingURLEncode(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, "a+b%26c%3Dd%2Fe", enc)
	back, err := EncodingURLDecode(ctx, enc)
	require.NoError(t, err)
	assert.Equal(t, in, back)
}

func TestEncodingDecodeErrors(t *testing.T) {
	ctx := context.Background()
	_, err := EncodingBase64Decode(ctx, "not base64!!!")
	assert.Error(t, err, "base64_decode of garbage should error")
	_, err = EncodingHexDecode(ctx, "zz")
	assert.Error(t, err, "hex_decode of garbage should error")
	_, err = EncodingURLDecode(ctx, "%zz")
	assert.Error(t, err, "url_decode of a malformed escape should error")
}
