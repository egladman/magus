package image

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"slices"

	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/json"
)

// AssembleIndex joins single-platform layouts into one Image Index at dest.
func AssembleIndex(ctx context.Context, layouts []string, dest string) (Layout, error) {
	if len(layouts) == 0 {
		return Layout{}, fmt.Errorf("image.index: no input layouts")
	}
	var manifests []ocispec.Descriptor
	var blobs []blob
	seen := map[Platform]bool{}
	for _, path := range layouts {
		if err := ctx.Err(); err != nil {
			return Layout{}, fmt.Errorf("image.index: %w", err)
		}
		input, err := readPlatformLayout(ctx, path)
		if err != nil {
			return Layout{}, fmt.Errorf("image.index: layout %q: %w", path, err)
		}
		cfg := input.config
		platform := Platform{OS: cfg.OS, Architecture: cfg.Architecture, Variant: cfg.Variant}
		if seen[platform] {
			return Layout{}, fmt.Errorf("image.index: duplicate platform %s/%s/%s", platform.OS, platform.Architecture, platform.Variant)
		}
		seen[platform] = true
		desc := input.descriptor
		desc.Platform = &ocispec.Platform{OS: platform.OS, Architecture: platform.Architecture, Variant: platform.Variant}
		manifests = append(manifests, desc)
		blobs = append(blobs, input.blobs...)
	}
	slices.SortFunc(manifests, func(a, b ocispec.Descriptor) int {
		if a.Platform.OS != b.Platform.OS {
			return cmp.Compare(a.Platform.OS, b.Platform.OS)
		}
		if a.Platform.Architecture != b.Platform.Architecture {
			return cmp.Compare(a.Platform.Architecture, b.Platform.Architecture)
		}
		return cmp.Compare(a.Platform.Variant, b.Platform.Variant)
	})
	indexData, err := json.Marshal(ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex, Manifests: manifests})
	if err != nil {
		return Layout{}, fmt.Errorf("image.index: marshal: %w", err)
	}
	root := blobDescriptor(ocispec.MediaTypeImageIndex, indexData)
	blobs = append(blobs, bytesBlob(root.MediaType, indexData))
	if err := writeLayout(ctx, dest, root, blobs); err != nil {
		return Layout{}, fmt.Errorf("image.index: %w", err)
	}
	return Layout{
		Path:       dest,
		Descriptor: root,
		Op:         OpAssembleIndex,
	}, nil
}

type platformLayout struct {
	descriptor ocispec.Descriptor
	config     ocispec.Image
	layers     []ocispec.Descriptor
	blobs      []blob
}

func readPlatformLayout(ctx context.Context, path string) (platformLayout, error) {
	if err := ctx.Err(); err != nil {
		return platformLayout{}, err
	}
	layout, err := OpenLayout(path)
	if err != nil {
		return platformLayout{}, err
	}
	if layout.Descriptor.MediaType != ocispec.MediaTypeImageManifest {
		return platformLayout{}, fmt.Errorf("expected a platform manifest, got %q", layout.Descriptor.MediaType)
	}
	manifestData, err := readBlob(path, layout.Descriptor)
	if err != nil {
		return platformLayout{}, fmt.Errorf("manifest: %w", err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return platformLayout{}, fmt.Errorf("manifest: %w", err)
	}
	configData, err := readBlob(path, manifest.Config)
	if err != nil {
		return platformLayout{}, fmt.Errorf("read config: %w", err)
	}
	var cfg ocispec.Image
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return platformLayout{}, fmt.Errorf("decode config: %w", err)
	}
	platform := Platform{OS: cfg.OS, Architecture: cfg.Architecture, Variant: cfg.Variant}
	if err := platform.Validate(); err != nil {
		return platformLayout{}, err
	}
	blobs := []blob{layoutBlob(path, layout.Descriptor), layoutBlob(path, manifest.Config)}
	for _, layer := range manifest.Layers {
		// Size+presence: full re-hash on every assemble is O(image); verified on ingest.
		if err := statLayoutBlob(path, layer); err != nil {
			return platformLayout{}, fmt.Errorf("layer: %w", err)
		}
		blobs = append(blobs, layoutBlob(path, layer))
	}
	return platformLayout{descriptor: layout.Descriptor, config: cfg, layers: manifest.Layers, blobs: blobs}, nil
}

// statLayoutBlob checks digest form, presence and size without reading bytes.
func statLayoutBlob(path string, desc ocispec.Descriptor) error {
	if !checkSHA256(desc.Digest) {
		return fmt.Errorf("blob digest %q is not a sha256 digest", desc.Digest)
	}
	info, err := os.Stat(blobPath(path, desc.Digest))
	if err != nil {
		return fmt.Errorf("blob %s: %w", desc.Digest, err)
	}
	if info.Size() != desc.Size {
		return fmt.Errorf("blob %s: size is %d bytes, descriptor says %d", desc.Digest, info.Size(), desc.Size)
	}
	return nil
}

func verifyLayoutBlob(ctx context.Context, path string, desc ocispec.Descriptor) error {
	if err := statLayoutBlob(path, desc); err != nil {
		return err
	}
	f, err := os.Open(blobPath(path, desc.Digest))
	if err != nil {
		return fmt.Errorf("blob %s: %w", desc.Digest, err)
	}
	defer f.Close()
	v := &verifier{want: desc, digester: digest.SHA256.Digester()}
	if _, err := io.Copy(v, &ctxReader{ctx: ctx, r: f}); err != nil {
		return fmt.Errorf("blob %s: %w", desc.Digest, err)
	}
	if err := v.check(); err != nil {
		return fmt.Errorf("blob %s: %w", desc.Digest, err)
	}
	return nil
}
