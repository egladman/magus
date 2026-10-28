package image

import (
	"context"
	"fmt"
	"io"
	"maps"

	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/libs/oci"
)

// Assemble writes the Image Manifest spec describes as an OCI Image Layout at dest: the
// base's layers, at most one new layer, then the config and the manifest. It never
// touches the network, and dest is left as it was when it fails. Every path is used as
// given; a caller that confines reads and writes checks them first. A Docker base is
// written back as OCI; a base with a non-distributable or unknown layer is refused.
func Assemble(ctx context.Context, spec Spec, dest string) (Manifest, error) {
	if err := spec.Validate(); err != nil {
		return Manifest{}, err
	}
	var base *baseImage
	if spec.Base != "" {
		b, err := openBase(ctx, spec.Base, spec.Platform)
		if err != nil {
			return Manifest{}, fmt.Errorf("image.assemble: base: %w", err)
		}
		base = &b
	}
	entries, err := layerEntries(ctx, spec)
	if err != nil {
		return Manifest{}, err
	}

	st, err := newStaging(dest)
	if err != nil {
		return Manifest{}, fmt.Errorf("image.assemble: %w", err)
	}
	out, err := assembleInto(ctx, st, spec, base, entries)
	if err != nil {
		st.discard()
		return Manifest{}, fmt.Errorf("image.assemble: %w", err)
	}
	if err := st.commit(out.Descriptor); err != nil {
		st.discard()
		return Manifest{}, fmt.Errorf("image.assemble: %w", err)
	}
	out.Path = dest
	return out, nil
}

// baseImage is the platform manifest an assemble builds over, its layers as OCI
// descriptors, its config decoded and as written, and the base.name to record.
type baseImage struct {
	manifest  Manifest
	layers    []ocispec.Descriptor
	config    ocispec.Image
	configRaw []byte
	name      string
}

func openBase(ctx context.Context, path string, p Platform) (baseImage, error) {
	opened, err := OpenLayout(ctx, path, "")
	if err != nil {
		return baseImage{}, err
	}
	resolved, err := opened.Manifest(ctx, p)
	if err != nil {
		return baseImage{}, err
	}
	layers, err := baseLayers(resolved.Layers)
	if err != nil {
		return baseImage{}, err
	}
	cfg, raw, err := opened.config(ctx, resolved.Config)
	if err != nil {
		return baseImage{}, err
	}
	return baseImage{
		manifest:  resolved,
		layers:    layers,
		config:    cfg,
		configRaw: raw,
		name:      baseName(opened.Descriptor, resolved.Descriptor.Digest),
	}, nil
}

// baseLayers describes each base layer by its OCI media type. Docker's tar+gzip layer is
// the same bytes as OCI's (image-spec media-types.md, compatibility matrix).
func baseLayers(in []ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	out := make([]ocispec.Descriptor, len(in))
	for i, l := range in {
		switch {
		case l.MediaType == ocispec.MediaTypeImageLayer, l.MediaType == ocispec.MediaTypeImageLayerGzip, l.MediaType == ocispec.MediaTypeImageLayerZstd:
		case l.MediaType == mediaTypeDockerLayerGzip:
			l.MediaType = ocispec.MediaTypeImageLayerGzip
		case isNonDistributable(l.MediaType):
			return nil, fmt.Errorf("layer %s is non-distributable (%s): magus assembles only over layers it can copy", l.Digest, l.MediaType)
		default:
			return nil, fmt.Errorf("layer %s has media type %q, which is not a layer magus can assemble over (tar, tar+gzip, tar+zstd)", l.Digest, l.MediaType)
		}
		out[i] = l
	}
	return out, nil
}

// baseName is the base.name annotation for an image built over manifest: the reference
// Pull recorded as the layout's ref.name, but only when that reference pins manifest
// itself. annotations.md pairs base.name with base.digest, so a name pinning an index
// would point a reader at another manifest than the digest beside it.
func baseName(root ocispec.Descriptor, manifest digest.Digest) string {
	name := root.Annotations[ocispec.AnnotationRefName]
	ref, err := oci.ParseReference(name)
	if err != nil || ref.Digest != manifest {
		return ""
	}
	return name
}

func assembleInto(ctx context.Context, st *staging, spec Spec, base *baseImage, entries []entry) (Manifest, error) {
	var layers []ocispec.Descriptor
	var baseConfig *ocispec.Image
	var baseConfigRaw []byte
	annotations := maps.Clone(spec.Annotations)
	if base != nil {
		if err := linkBase(ctx, st, base); err != nil {
			return Manifest{}, err
		}
		layers, baseConfig, baseConfigRaw = base.layers, &base.config, base.configRaw
		annotations = base.annotate(annotations)
	}
	var added *Layer
	var diffID digest.Digest
	if len(entries) > 0 {
		a, err := addLayer(ctx, st, entries)
		if err != nil {
			return Manifest{}, err
		}
		added, layers, diffID = &a, append(layers, a.Descriptor), a.DiffID
	}
	return writeManifest(st, spec, baseConfig, baseConfigRaw, diffID, layers, annotations, added)
}

func linkBase(ctx context.Context, st *staging, base *baseImage) error {
	for _, l := range base.layers {
		if err := st.link(ctx, base.manifest.Path, l); err != nil {
			return fmt.Errorf("base: %w", err)
		}
	}
	return nil
}

// annotate adds the base.digest and, when known, base.name annotations to a. Both name
// the platform manifest the image is built over.
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
func addLayer(ctx context.Context, st *staging, entries []entry) (Layer, error) {
	var blob layerBlob
	desc, err := st.write(func(w io.Writer) (ocispec.Descriptor, error) {
		var err error
		blob, err = writeLayer(ctx, w, entries, maxLayerBytes)
		return blob.descriptor, err
	})
	if err != nil {
		return Layer{}, err
	}
	added := Layer{Descriptor: desc, DiffID: blob.diffID}
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

func writeManifest(st *staging, spec Spec, baseConfig *ocispec.Image, baseConfigRaw []byte, diffID digest.Digest, layers []ocispec.Descriptor, annotations map[string]string, added *Layer) (Manifest, error) {
	cfg, err := buildConfig(baseConfig, spec, diffID)
	if err != nil {
		return Manifest{}, err
	}
	cfgData, err := configJSON(cfg, baseConfigRaw)
	if err != nil {
		return Manifest{}, fmt.Errorf("encode the image config: %w", err)
	}
	cfgDesc, err := st.put(ocispec.MediaTypeImageConfig, cfgData)
	if err != nil {
		return Manifest{}, err
	}
	manifestDesc, err := st.putJSON(ocispec.MediaTypeImageManifest, ocispec.Manifest{
		Versioned:   specs.Versioned{SchemaVersion: 2},
		MediaType:   ocispec.MediaTypeImageManifest,
		Config:      cfgDesc,
		Layers:      layers,
		Annotations: annotations,
	})
	if err != nil {
		return Manifest{}, err
	}
	manifestDesc.Platform = spec.Platform.oci()
	out := Manifest{
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
