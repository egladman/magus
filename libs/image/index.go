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
// on the order of layouts. Each input's referrers, such as a per-platform SBOM, are kept
// in the layout beside the index, and Push sends them. It never touches the network.
func AssembleIndex(ctx context.Context, layouts []string, dest string) (Layout, error) {
	if len(layouts) == 0 {
		return Layout{}, fmt.Errorf("image.index: no input layouts")
	}
	var inputs []Manifest
	seen := map[string]string{}
	for _, p := range layouts {
		opened, err := OpenLayout(ctx, p, "")
		if err != nil {
			return Layout{}, fmt.Errorf("image.index: %w", err)
		}
		if opened.Descriptor.MediaType != ocispec.MediaTypeImageManifest {
			return Layout{}, fmt.Errorf("image.index: %s holds %s, but each input is one platform's OCI image manifest", p, opened.Descriptor.MediaType)
		}
		m, err := opened.manifest(ctx, opened.Descriptor)
		if err != nil {
			return Layout{}, fmt.Errorf("image.index: %w", err)
		}
		if prev, dup := seen[m.Platform.Normalize().String()]; dup {
			return Layout{}, fmt.Errorf("image.index: %s and %s are both %s", prev, p, m.Platform)
		}
		seen[m.Platform.Normalize().String()] = p
		inputs = append(inputs, m)
	}

	st, err := newStaging(dest)
	if err != nil {
		return Layout{}, fmt.Errorf("image.index: %w", err)
	}
	out, err := indexInto(ctx, st, inputs)
	if err == nil {
		err = st.commit(append([]ocispec.Descriptor{out.Descriptor}, out.Referrers...)...)
	}
	if err != nil {
		st.discard()
		return Layout{}, fmt.Errorf("image.index: %w", err)
	}
	out.Path = dest
	return out, nil
}

func indexInto(ctx context.Context, st *staging, inputs []Manifest) (Layout, error) {
	manifests := make([]ocispec.Descriptor, 0, len(inputs))
	var referrers []ocispec.Descriptor
	for _, in := range inputs {
		for _, d := range append([]ocispec.Descriptor{in.Descriptor, in.Config}, in.Layers...) {
			if err := st.link(ctx, in.Path, d); err != nil {
				return Layout{}, err
			}
		}
		for _, r := range in.Referrers {
			if err := copyImage(ctx, st, Layout{Path: in.Path, Descriptor: r}); err != nil {
				return Layout{}, err
			}
		}
		referrers = append(referrers, in.Referrers...)
		desc := in.Descriptor
		desc.Annotations = nil
		desc.Platform = in.Platform.oci()
		manifests = append(manifests, desc)
	}
	slices.SortFunc(referrers, func(a, b ocispec.Descriptor) int { return cmp.Compare(a.Digest, b.Digest) })
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
	return Layout{Descriptor: root, Referrers: referrers}, err
}
