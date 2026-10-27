package image

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPullRequiresDigest(t *testing.T) {
	_, err := PullLayout(context.Background(), "ghcr.io/egladman/magus:latest", NewLinuxAmd64(), t.TempDir(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sha256")
}

func TestPublishOffline(t *testing.T) {
	t.Setenv("MAGUS_OFFLINE", "1")
	_, err := PublishLayout(context.Background(), t.TempDir(), "ghcr.io/egladman/magus", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MAGUS_OFFLINE")
}

func TestPickPlatform(t *testing.T) {
	want := NewLinuxArm64()
	got, err := pickPlatform([]ocispec.Descriptor{
		{Platform: &ocispec.Platform{OS: "linux", Architecture: "amd64"}},
		{Digest: "sha256:abc", Platform: &ocispec.Platform{OS: "linux", Architecture: "arm64"}},
	}, want)
	require.NoError(t, err)
	assert.Equal(t, "sha256:abc", got.Digest.String())

	_, err = pickPlatform(nil, want)
	require.Error(t, err)
}

func TestMediaTypeKind(t *testing.T) {
	assert.Equal(t, "index", mediaTypeKind(ocispec.MediaTypeImageIndex))
	assert.Equal(t, "manifest", mediaTypeKind(ocispec.MediaTypeImageManifest))
	assert.Equal(t, "index", mediaTypeKind("application/vnd.docker.distribution.manifest.list.v2+json"))
}

func TestWritePerPlatformThenIndex(t *testing.T) {
	root := t.TempDir()
	var layouts []string
	for _, p := range []Platform{NewLinuxAmd64(), NewLinuxArm64()} {
		bin := filepath.Join(root, p.Architecture+".bin")
		require.NoError(t, os.WriteFile(bin, []byte("hi-"+p.Architecture), 0o644))
		l, err := AssembleLayout(context.Background(), Spec{
			Platform: p,
			Files:    []File{{Source: bin, Destination: "/app/bin", Mode: 0o755}},
		}, filepath.Join(root, "out", p.Architecture))
		require.NoError(t, err)
		assert.Equal(t, OpAssembleLayout, l.Op)
		assert.NotEmpty(t, l.Descriptor.Digest)
		layouts = append(layouts, l.Path)
	}
	idx, err := AssembleIndex(context.Background(), layouts, filepath.Join(root, "index"))
	require.NoError(t, err)
	assert.Equal(t, ocispec.MediaTypeImageIndex, idx.Descriptor.MediaType)
	assert.Equal(t, OpAssembleIndex, idx.Op)
}
