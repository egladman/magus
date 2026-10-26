package image

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Write assembles one OCI image layout from a spec.
func Write(ctx context.Context, spec Spec, dest string) (Layout, error) {
	if err := spec.Validate(); err != nil {
		return Layout{}, err
	}
	if err := ctx.Err(); err != nil {
		return Layout{}, fmt.Errorf("image.write: %w", err)
	}

	var base *ocispec.Image
	var baseLayers []ocispec.Descriptor
	var blobs []blob
	if spec.Base != "" {
		input, err := readPlatformLayout(ctx, spec.Base)
		if err != nil {
			return Layout{}, fmt.Errorf("image.write: base: %w", err)
		}
		cfg := input.config
		if cfg.OS != spec.Platform.OS || cfg.Architecture != spec.Platform.Architecture || cfg.Variant != spec.Platform.Variant {
			return Layout{}, fmt.Errorf("image.write: base config platform %s/%s/%s differs from requested %s/%s/%s", cfg.OS, cfg.Architecture, cfg.Variant, spec.Platform.OS, spec.Platform.Architecture, spec.Platform.Variant)
		}
		base = &cfg
		baseLayers, blobs = input.layers, input.blobs
	}

	var newLayer *ocispec.Descriptor
	var diffID digest.Digest
	if len(spec.Files)+len(spec.Symlinks) > 0 {
		layerPath, layer, err := stageLayer(ctx, spec, dest)
		if err != nil {
			return Layout{}, err
		}
		defer os.Remove(layerPath)
		newLayer = &layer.descriptor
		diffID = layer.diffID
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
	return Layout{Path: dest, Descriptor: manifestDesc, Platform: spec.Platform}, nil
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
		return "", layerBlob{}, fmt.Errorf("image.write: layer temp: %w", err)
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
		return "", layerBlob{}, fmt.Errorf("image.write: layer temp: %w", closeErr)
	}
	return f.Name(), layer, nil
}
