package hex

import (
	"context"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestEncodingHexRoundTrip(t *testing.T) {
	ctx := context.Background()
	enc, err := HexEncode(ctx, "abc")
	require.NoError(t, err)
	assert.Equal(t, "616263", enc)
	back, err := HexDecode(ctx, enc)
	require.NoError(t, err)
	assert.Equal(t, "abc", back)
}

func TestEncodingHexDecodeErrors(t *testing.T) {
	ctx := context.Background()
	_, err := HexDecode(ctx, "zz")
	assert.Error(t, err, "hex_decode of garbage should error")
}
