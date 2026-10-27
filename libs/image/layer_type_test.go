package image

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLayerReturnsOCIDigests(t *testing.T) {
	src := filepath.Join(t.TempDir(), "f")
	require.NoError(t, os.WriteFile(src, []byte("layer-bytes"), 0o600))
	var buf bytes.Buffer
	layer, err := NewLayer(context.Background(), &buf, []File{{
		Source: src, Destination: "/f", Mode: 0o644,
	}}, nil)
	require.NoError(t, err)
	assert.False(t, layer.IsZero())
	assert.Equal(t, "sha256", layer.Digest.Algorithm())
	assert.Equal(t, "sha256", layer.DiffID.Algorithm())
	assert.NotEqual(t, layer.Digest.String(), layer.DiffID.String())
	assert.Equal(t, ocispec.MediaTypeImageLayerGzip, layer.MediaType)
	assert.Equal(t, int64(buf.Len()), layer.Size)
}
