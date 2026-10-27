package image

import (
	"context"
	"fmt"
	"io"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Layer is one OCI image layer: a gzipped tar filesystem diff.
// Manifest.layers carries Digest; config.rootfs.diff_ids carries DiffID.
// This is the OCI term — not a Dockerfile instruction and not a build stage.
type Layer struct {
	Digest    Digest // compressed blob (Descriptor.Digest)
	DiffID    DiffID // uncompressed tar (config.rootfs.diff_ids)
	Size      int64
	MediaType string
}

// IsZero reports an absent layer (config-only assemble, or unset Layout.Layer).
func (l Layer) IsZero() bool { return l.Digest.IsZero() }

// NewLayer writes one OCI layer to w from file and symlink placements and
// returns its digests. At most one Layer is what AssembleLayout adds over a base.
func NewLayer(ctx context.Context, w io.Writer, files []File, symlinks []Symlink) (Layer, error) {
	if w == nil {
		return Layer{}, fmt.Errorf("image.layer: nil writer")
	}
	blob, err := writeLayer(ctx, w, Spec{Files: files, Symlinks: symlinks}, maxLayerBytes)
	if err != nil {
		return Layer{}, err
	}
	return layerFromBlob(blob), nil
}

func layerFromBlob(b layerBlob) Layer {
	mt := b.descriptor.MediaType
	if mt == "" {
		mt = ocispec.MediaTypeImageLayerGzip
	}
	return Layer{
		Digest:    MustDigest(b.descriptor.Digest),
		DiffID:    MustDiffID(b.diffID),
		Size:      b.descriptor.Size,
		MediaType: mt,
	}
}
