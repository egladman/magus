package url

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodingURLRoundTrip(t *testing.T) {
	ctx := context.Background()
	const in = "a b&c=d/e"
	enc, err := URLEncode(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, "a+b%26c%3Dd%2Fe", enc)
	back, err := URLDecode(ctx, enc)
	require.NoError(t, err)
	assert.Equal(t, in, back)
}

func TestEncodingURLDecodeErrors(t *testing.T) {
	ctx := context.Background()
	_, err := URLDecode(ctx, "%zz")
	assert.Error(t, err, "url_decode of a malformed escape should error")
}
