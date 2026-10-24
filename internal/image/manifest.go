package image

import (
	"fmt"
	"maps"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/json"
)

// blobDescriptor describes data under mediaType: its sha256 digest and length.
func blobDescriptor(mediaType string, data []byte) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
}

// buildManifest lists the base layers verbatim (R15), then newLayer when the
// write added one (R9). Annotations are exactly the spec's (R20).
func buildManifest(config ocispec.Descriptor, baseLayers []ocispec.Descriptor, newLayer *ocispec.Descriptor, annotations map[string]string) ocispec.Manifest {
	layers := make([]ocispec.Descriptor, 0, len(baseLayers)+1)
	layers = append(layers, baseLayers...)
	if newLayer != nil {
		layers = append(layers, *newLayer)
	}
	m := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    config,
		Layers:    layers,
	}
	if len(annotations) > 0 {
		m.Annotations = maps.Clone(annotations)
	}
	return m
}

// manifestBlob marshals m (R19) and describes the bytes.
func manifestBlob(m ocispec.Manifest) ([]byte, ocispec.Descriptor, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, ocispec.Descriptor{}, fmt.Errorf("image.write: manifest: %w", err)
	}
	return data, blobDescriptor(ocispec.MediaTypeImageManifest, data), nil
}
