package image

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/json"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func imageTestPlatformLayout(t *testing.T, p Platform) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(src, []byte(p.Architecture), 0o600))
	dest := filepath.Join(t.TempDir(), "layout")
	_, err := AssembleLayout(context.Background(), Spec{Platform: p, Files: []File{{Source: src, Destination: "/file", Mode: 0o644}}}, dest)
	require.NoError(t, err)
	return dest
}

func TestImageIndexIndependentReaderAndOrdering(t *testing.T) {
	arm := imageTestPlatformLayout(t, Platform{OS: "linux", Architecture: "arm64"})
	amd := imageTestPlatformLayout(t, Platform{OS: "linux", Architecture: "amd64"})
	dest := filepath.Join(t.TempDir(), "index")
	got, err := AssembleIndex(context.Background(), []string{arm, amd}, dest)
	require.NoError(t, err)
	assert.Equal(t, ocispec.MediaTypeImageIndex, got.Descriptor.MediaType)
	assert.Equal(t, Platform{}, got.Platform)
	marker, err := os.ReadFile(filepath.Join(dest, "oci-layout"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"imageLayoutVersion":"1.0.0"}`, string(marker))
	raw, err := os.ReadFile(filepath.Join(dest, "index.json"))
	require.NoError(t, err)
	var layoutIndex ocispec.Index
	require.NoError(t, json.Unmarshal(raw, &layoutIndex))
	require.Len(t, layoutIndex.Manifests, 1)
	assert.Equal(t, got.Descriptor, layoutIndex.Manifests[0])
	var index ocispec.Index
	require.NoError(t, json.Unmarshal(imageTestReadBlob(t, dest, got.Descriptor), &index))
	require.Len(t, index.Manifests, 2)
	assert.Equal(t, "amd64", index.Manifests[0].Platform.Architecture)
	assert.Equal(t, "arm64", index.Manifests[1].Platform.Architecture)
	for _, desc := range index.Manifests {
		var manifest ocispec.Manifest
		require.NoError(t, json.Unmarshal(imageTestReadBlob(t, dest, desc), &manifest))
		var cfg ocispec.Image
		require.NoError(t, json.Unmarshal(imageTestReadBlob(t, dest, manifest.Config), &cfg))
		assert.Equal(t, desc.Platform.Architecture, cfg.Architecture)
		require.Len(t, manifest.Layers, 1)
		entries, diffID := imageTestLayerEntries(t, imageTestReadBlob(t, dest, manifest.Layers[0]))
		assert.Equal(t, []digest.Digest{diffID}, cfg.RootFS.DiffIDs)
		assert.Equal(t, []string{"file"}, []string{entries[0].Name})
	}
	reversed := filepath.Join(t.TempDir(), "reversed")
	other, err := AssembleIndex(context.Background(), []string{amd, arm}, reversed)
	require.NoError(t, err)
	assert.Equal(t, got.Descriptor, other.Descriptor)
	compareImageTrees(t, dest, reversed)
}

func TestImageIndexRejectsInvalidInputs(t *testing.T) {
	_, err := AssembleIndex(context.Background(), nil, filepath.Join(t.TempDir(), "none"))
	require.ErrorContains(t, err, "no input")
	amd := imageTestPlatformLayout(t, Platform{OS: "linux", Architecture: "amd64"})
	_, err = AssembleIndex(context.Background(), []string{amd, amd}, filepath.Join(t.TempDir(), "duplicate"))
	require.ErrorContains(t, err, "duplicate platform")
	index := filepath.Join(t.TempDir(), "index")
	_, err = AssembleIndex(context.Background(), []string{amd}, index)
	require.NoError(t, err)
	_, err = AssembleIndex(context.Background(), []string{index}, filepath.Join(t.TempDir(), "nested"))
	require.ErrorContains(t, err, "platform manifest")
	_, manifest, _ := imageTestReadManifest(t, amd)
	layer := manifest.Layers[0]
	require.NoError(t, os.WriteFile(filepath.Join(amd, "blobs", "sha256", layer.Digest.Encoded()), []byte("broken"), 0o644))
	_, err = AssembleIndex(context.Background(), []string{amd}, filepath.Join(t.TempDir(), "corrupt"))
	require.ErrorContains(t, err, "layer")
}
