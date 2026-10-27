package image

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"

	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Assemble writes the Image Manifest spec describes as an OCI Image Layout at dest: the
// base's layers, at most one new layer, then the config and the manifest. It never
// touches the network, and dest is left as it was when it fails. Every path is used as
// given; a caller that confines reads and writes checks them first.
func Assemble(ctx context.Context, spec Spec, dest string) (ManifestLayout, error) {
	if err := spec.Validate(); err != nil {
		return ManifestLayout{}, err
	}
	var base *baseImage
	if spec.Base != "" {
		b, err := openBase(ctx, spec.Base, spec.Platform)
		if err != nil {
			return ManifestLayout{}, fmt.Errorf("image.assemble: base: %w", err)
		}
		base = &b
	}
	entries, err := layerEntries(ctx, spec)
	if err != nil {
		return ManifestLayout{}, err
	}

	st, err := newStaging(dest)
	if err != nil {
		return ManifestLayout{}, fmt.Errorf("image.assemble: %w", err)
	}
	out, err := assembleInto(ctx, st, spec, base, entries)
	if err != nil {
		st.discard()
		return ManifestLayout{}, fmt.Errorf("image.assemble: %w", err)
	}
	if err := st.commit(out.Descriptor); err != nil {
		st.discard()
		return ManifestLayout{}, fmt.Errorf("image.assemble: %w", err)
	}
	out.Path = dest
	return out, nil
}

// baseImage is the platform manifest an assemble builds over, its config, and the
// reference Pull recorded for the layout, if any.
type baseImage struct {
	manifest ManifestLayout
	config   ocispec.Image
	name     string
}

func openBase(ctx context.Context, path string, p Platform) (baseImage, error) {
	opened, err := OpenLayout(ctx, path)
	if err != nil {
		return baseImage{}, err
	}
	resolved, err := opened.ResolvePlatform(ctx, p)
	if err != nil {
		return baseImage{}, err
	}
	cfg, err := opened.config(ctx, resolved.Config)
	if err != nil {
		return baseImage{}, err
	}
	return baseImage{manifest: resolved, config: cfg, name: opened.Descriptor.Annotations[ocispec.AnnotationRefName]}, nil
}

func assembleInto(ctx context.Context, st *staging, spec Spec, base *baseImage, entries []entry) (ManifestLayout, error) {
	var layers []ocispec.Descriptor
	var baseConfig *ocispec.Image
	annotations := maps.Clone(spec.Annotations)
	if base != nil {
		if err := linkBase(ctx, st, base); err != nil {
			return ManifestLayout{}, err
		}
		layers, baseConfig = slices.Clone(base.manifest.Layers), &base.config
		annotations = base.annotate(annotations)
	}
	var added *Added
	var diffID digest.Digest
	if len(entries) > 0 {
		a, err := addLayer(ctx, st, entries)
		if err != nil {
			return ManifestLayout{}, err
		}
		added, layers, diffID = &a, append(layers, a.Layer), a.DiffID
	}
	return writeManifest(st, spec, baseConfig, diffID, layers, annotations, added)
}

func linkBase(ctx context.Context, st *staging, base *baseImage) error {
	for _, l := range base.manifest.Layers {
		if err := st.link(ctx, base.manifest.Path, l); err != nil {
			return fmt.Errorf("base: %w", err)
		}
	}
	return nil
}

// annotate adds the base.digest and, when known, base.name annotations to a.
func (b *baseImage) annotate(a map[string]string) map[string]string {
	if a == nil {
		a = map[string]string{}
	}
	a[ocispec.AnnotationBaseImageDigest] = b.manifest.Descriptor.Digest.String()
	if b.name != "" {
		a[ocispec.AnnotationBaseImageName] = b.name
	}
	return a
}

// addLayer writes entries as the new layer.
func addLayer(ctx context.Context, st *staging, entries []entry) (Added, error) {
	var blob layerBlob
	desc, err := st.write(func(w io.Writer) (ocispec.Descriptor, error) {
		var err error
		blob, err = writeLayer(ctx, w, entries, maxLayerBytes)
		return blob.descriptor, err
	})
	if err != nil {
		return Added{}, err
	}
	added := Added{Layer: desc, DiffID: blob.diffID}
	for _, e := range entries {
		switch e.kind {
		case kindFile:
			added.Files++
		case kindSymlink:
			added.Symlinks++
		case kindDirectory:
			added.Directories++
		}
	}
	return added, nil
}

func writeManifest(st *staging, spec Spec, baseConfig *ocispec.Image, diffID digest.Digest, layers []ocispec.Descriptor, annotations map[string]string, added *Added) (ManifestLayout, error) {
	cfg, err := buildConfig(baseConfig, spec, diffID)
	if err != nil {
		return ManifestLayout{}, err
	}
	cfgData, err := configJSON(cfg)
	if err != nil {
		return ManifestLayout{}, fmt.Errorf("config: %w", err)
	}
	cfgDesc, err := st.put(ocispec.MediaTypeImageConfig, cfgData)
	if err != nil {
		return ManifestLayout{}, err
	}
	manifestDesc, _, err := st.putJSON(ocispec.MediaTypeImageManifest, ocispec.Manifest{
		Versioned:   specs.Versioned{SchemaVersion: 2},
		MediaType:   ocispec.MediaTypeImageManifest,
		Config:      cfgDesc,
		Layers:      layers,
		Annotations: annotations,
	})
	if err != nil {
		return ManifestLayout{}, err
	}
	manifestDesc.Platform = spec.Platform.oci()
	out := ManifestLayout{
		Layout:   Layout{Descriptor: manifestDesc},
		Platform: spec.Platform,
		Config:   cfgDesc,
		Layers:   layers,
		Added:    added,
	}
	if cfg.Created != nil {
		out.Created = cfg.Created.Format(timeLayout)
	}
	return out, nil
}
