package image

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// AssembleLayout builds one OCI Image Layout from a Spec: optional base layers,
// at most one new Layer for files and symlinks, then config and Image Manifest.
// Layer is the OCI filesystem-diff primitive; AssembleLayout is the layout verb.
func AssembleLayout(ctx context.Context, spec Spec, dest string) (Layout, error) {
	out := &Layout{Op: OpAssembleLayout, Base: spec.Base, Platform: spec.Platform}
	if err := spec.expandDirs(ctx); err != nil {
		return Layout{}, err
	}
	if err := spec.Validate(); err != nil {
		return Layout{}, err
	}
	if err := ctx.Err(); err != nil {
		return Layout{}, fmt.Errorf("image.assemble: %w", err)
	}
	// Layer and layout staging land beside dest; CreateTemp needs that parent.
	if err := os.MkdirAll(filepath.Dir(filepath.Clean(dest)), 0o755); err != nil {
		return Layout{}, fmt.Errorf("image.assemble: %w", err)
	}

	var base *ocispec.Image
	var baseLayers []ocispec.Descriptor
	var blobs []blob
	if spec.Base != "" {
		input, err := readPlatformLayout(ctx, spec.Base)
		if err != nil {
			return Layout{}, fmt.Errorf("image.assemble: base %q: %w (local layout only; refresh with image.pullLayout under the update charm)", spec.Base, err)
		}
		cfg := input.config
		if cfg.OS != spec.Platform.OS || cfg.Architecture != spec.Platform.Architecture || cfg.Variant != spec.Platform.Variant {
			return Layout{}, fmt.Errorf("image.assemble: base config platform %s/%s/%s differs from requested %s/%s/%s", cfg.OS, cfg.Architecture, cfg.Variant, spec.Platform.OS, spec.Platform.Architecture, spec.Platform.Variant)
		}
		base = &cfg
		baseLayers, blobs = input.layers, input.blobs
		out.BaseDigest = MustDigest(input.descriptor.Digest)
		setCreated(out, cfg.Created)
	}

	var newLayer *ocispec.Descriptor
	var diffID digest.Digest
	var added Layer
	if len(spec.Files)+len(spec.Symlinks)+len(spec.emptyDirs) > 0 {
		layerPath, layer, err := stageLayer(ctx, spec, dest)
		if err != nil {
			return Layout{}, err
		}
		defer os.Remove(layerPath)
		newLayer = &layer.descriptor
		diffID = layer.diffID
		added = layerFromBlob(layer)
		blobs = append(blobs, blob{
			desc: layer.descriptor,
			open: func() (io.ReadCloser, error) { return os.Open(layerPath) },
		})
	}

	manifestDesc, metadata, err := imageMetadata(base, spec, diffID, baseLayers, newLayer)
	if err != nil {
		return Layout{}, err
	}
	blobs = append(blobs, metadata...)
	if err := writeLayout(ctx, dest, manifestDesc, blobs); err != nil {
		return Layout{}, err
	}
	out.Path = dest
	out.Descriptor = manifestDesc
	out.FileCount = len(spec.Files)
	out.SymlinkCount = len(spec.Symlinks)
	out.Layer = added
	if spec.Created != "" {
		t, err := time.Parse(time.RFC3339, spec.Created)
		if err != nil {
			return Layout{}, fmt.Errorf("image.assemble: created %q: want RFC 3339: %w", spec.Created, err)
		}
		setCreated(out, &t)
	}
	for _, b := range blobs {
		if b.desc.MediaType == ocispec.MediaTypeImageConfig {
			out.ConfigDigest = MustDigest(b.desc.Digest)
			break
		}
	}
	return *out, nil
}

func imageMetadata(base *ocispec.Image, spec Spec, diffID digest.Digest, baseLayers []ocispec.Descriptor, layer *ocispec.Descriptor) (ocispec.Descriptor, []blob, error) {
	cfg, err := buildConfig(base, spec, diffID)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	configData, configDesc, err := configBlob(cfg)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	manifest := buildManifest(configDesc, baseLayers, layer, spec.Annotations)
	manifestData, manifestDesc, err := manifestBlob(manifest)
	if err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	manifestDesc.Platform = &ocispec.Platform{OS: spec.Platform.OS, Architecture: spec.Platform.Architecture, Variant: spec.Platform.Variant}
	return manifestDesc, []blob{bytesBlob(configDesc.MediaType, configData), bytesBlob(manifestDesc.MediaType, manifestData)}, nil
}

func stageLayer(ctx context.Context, spec Spec, dest string) (path string, layer layerBlob, err error) {
	f, err := os.CreateTemp(filepath.Dir(filepath.Clean(dest)), ".image-layer-*")
	if err != nil {
		return "", layerBlob{}, fmt.Errorf("image.layer: temp: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}()
	layer, writeErr := writeLayer(ctx, f, spec, maxLayerBytes)
	closeErr := f.Close()
	if writeErr != nil {
		return "", layerBlob{}, writeErr
	}
	if closeErr != nil {
		return "", layerBlob{}, fmt.Errorf("image.layer: temp: %w", closeErr)
	}
	return f.Name(), layer, nil
}
