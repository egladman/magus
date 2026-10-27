package image

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// AssembleIndex writes one Image Index over single-platform layouts as a layout at dest.
// Each input's image must be one Image Manifest; two inputs for the same platform, once
// normalized, are refused. Entries sort by platform, so the index digest does not depend
// on the order of layouts. It never touches the network.
func AssembleIndex(ctx context.Context, layouts []string, dest string) (Layout, error) {
	if len(layouts) == 0 {
		return Layout{}, fmt.Errorf("image.index: no input layouts")
	}
	var inputs []ManifestLayout
	seen := map[Platform]string{}
	for _, p := range layouts {
		opened, err := OpenLayout(ctx, p)
		if err != nil {
			return Layout{}, fmt.Errorf("image.index: %w", err)
		}
		if opened.Descriptor.MediaType != ocispec.MediaTypeImageManifest {
			return Layout{}, fmt.Errorf("image.index: %s holds an index, but each input is one platform's manifest", p)
		}
		m, err := opened.manifest(ctx, opened.Descriptor)
		if err != nil {
			return Layout{}, fmt.Errorf("image.index: %w", err)
		}
		if prev, dup := seen[m.Platform.Normalize()]; dup {
			return Layout{}, fmt.Errorf("image.index: %s and %s are both %s", prev, p, m.Platform)
		}
		seen[m.Platform.Normalize()] = p
		inputs = append(inputs, m)
	}

	st, err := newStaging(dest)
	if err != nil {
		return Layout{}, fmt.Errorf("image.index: %w", err)
	}
	root, err := indexInto(ctx, st, inputs)
	if err == nil {
		err = st.commit(root)
	}
	if err != nil {
		st.discard()
		return Layout{}, fmt.Errorf("image.index: %w", err)
	}
	return Layout{Path: dest, Descriptor: root}, nil
}

func indexInto(ctx context.Context, st *staging, inputs []ManifestLayout) (ocispec.Descriptor, error) {
	manifests := make([]ocispec.Descriptor, 0, len(inputs))
	for _, in := range inputs {
		for _, d := range append([]ocispec.Descriptor{in.Descriptor, in.Config}, in.Layers...) {
			if err := st.link(ctx, in.Path, d); err != nil {
				return ocispec.Descriptor{}, err
			}
		}
		desc := in.Descriptor
		desc.Annotations = nil
		desc.Platform = in.Platform.oci()
		manifests = append(manifests, desc)
	}
	slices.SortFunc(manifests, func(a, b ocispec.Descriptor) int {
		return cmp.Or(
			cmp.Compare(a.Platform.OS, b.Platform.OS),
			cmp.Compare(a.Platform.Architecture, b.Platform.Architecture),
			cmp.Compare(a.Platform.Variant, b.Platform.Variant),
		)
	})
	root, err := st.putJSON(ocispec.MediaTypeImageIndex, ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: manifests,
	})
	return root, err
}
