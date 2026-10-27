package image

import (
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func imageTestDescriptor(mediaType, hex string, size int64) ocispec.Descriptor {
	return ocispec.Descriptor{MediaType: mediaType, Digest: digest.Digest("sha256:" + hex), Size: size}
}

func TestImageManifestLayers(t *testing.T) {
	cfg := imageTestDescriptor(ocispec.MediaTypeImageConfig, "11", 5)
	base := []ocispec.Descriptor{
		imageTestDescriptor(ocispec.MediaTypeImageLayerGzip, "22", 10),
		imageTestDescriptor(ocispec.MediaTypeImageLayerZstd, "33", 20),
	}
	added := imageTestDescriptor(ocispec.MediaTypeImageLayerGzip, "44", 30)

	with := buildManifest(cfg, base, &added, nil)
	assert.Equal(t, ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    cfg,
		Layers:    []ocispec.Descriptor{base[0], base[1], added},
	}, with)

	without := buildManifest(cfg, base, nil, nil)
	assert.Equal(t, base, without.Layers)

	scratch := buildManifest(cfg, nil, nil, nil)
	assert.NotNil(t, scratch.Layers)
	assert.Empty(t, scratch.Layers)
}

func TestImageManifestAnnotations(t *testing.T) {
	cfg := imageTestDescriptor(ocispec.MediaTypeImageConfig, "11", 5)

	none := buildManifest(cfg, nil, nil, nil)
	assert.Nil(t, none.Annotations)

	ann := map[string]string{"k": "v"}
	got := buildManifest(cfg, nil, nil, ann)
	assert.Equal(t, map[string]string{"k": "v"}, got.Annotations)
	ann["k"] = "changed"
	assert.Equal(t, "v", got.Annotations["k"])
}

func TestImageManifestBlobBytes(t *testing.T) {
	cfg := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageConfig, Digest: digest.Digest("sha256:aa"), Size: 7}
	layer := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageLayerGzip, Digest: digest.Digest("sha256:bb"), Size: 9}
	m := buildManifest(cfg, nil, &layer, map[string]string{"a": "b"})

	data, desc, err := manifestBlob(m)
	require.NoError(t, err)

	assert.Equal(t, `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:aa","size":7},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:bb","size":9}],"annotations":{"a":"b"}}`, string(data))
	assert.Equal(t, ocispec.Descriptor{
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}, desc)
}

func TestImageManifestBlobDescriptor(t *testing.T) {
	got := blobDescriptor("x/y", []byte("abc"))
	assert.Equal(t, ocispec.Descriptor{
		MediaType: "x/y",
		Digest:    digest.Digest("sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"),
		Size:      3,
	}, got)
}
