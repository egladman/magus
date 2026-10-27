package image

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/json"
)

// Layout is an OCI Image Layout on disk. index.json's first descriptor is the image, an
// Image Manifest or an Image Index; any after it are referrers, artifact manifests whose
// subject is that image (AttachReferrers writes them).
type Layout struct {
	Path       string
	Descriptor ocispec.Descriptor
	Referrers  []ocispec.Descriptor
}

// ManifestLayout is a Layout whose image is one Image Manifest, with the facts of that
// manifest and its config.
type ManifestLayout struct {
	Layout
	Platform Platform
	Config   ocispec.Descriptor
	Layers   []ocispec.Descriptor
	// Added is the layer Assemble wrote, nil when it wrote none or the layout was not
	// assembled.
	Added *Added
	// Created is the config's created time as RFC 3339, empty when the config omits it.
	Created string
}

// OpenLayout reads the layout at path and verifies the image's root blob against its
// descriptor. A missing layout wraps fs.ErrNotExist.
func OpenLayout(ctx context.Context, path string) (Layout, error) {
	if err := ctx.Err(); err != nil {
		return Layout{}, err
	}
	marker, err := os.ReadFile(filepath.Join(path, "oci-layout"))
	if err != nil {
		return Layout{}, fmt.Errorf("image.open: %w", err)
	}
	var m ocispec.ImageLayout
	if err := json.Unmarshal(marker, &m); err != nil {
		return Layout{}, fmt.Errorf("image.open: %s: oci-layout: %w", path, err)
	}
	if m.Version != layoutVersion {
		return Layout{}, fmt.Errorf("image.open: %s: oci-layout version %q, want %q", path, m.Version, layoutVersion)
	}
	raw, err := os.ReadFile(filepath.Join(path, "index.json"))
	if err != nil {
		return Layout{}, fmt.Errorf("image.open: %w", err)
	}
	var idx ocispec.Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return Layout{}, fmt.Errorf("image.open: %s: index.json: %w", path, err)
	}
	if len(idx.Manifests) == 0 {
		return Layout{}, fmt.Errorf("image.open: %s: index.json names no image", path)
	}
	l := Layout{Path: path, Descriptor: idx.Manifests[0], Referrers: idx.Manifests[1:]}
	switch l.Descriptor.MediaType {
	case ocispec.MediaTypeImageManifest, ocispec.MediaTypeImageIndex:
	default:
		return Layout{}, fmt.Errorf("image.open: %s: the image is %q, want an OCI image manifest or index", path, l.Descriptor.MediaType)
	}
	if _, err := l.readBlob(ctx, l.Descriptor); err != nil {
		return Layout{}, fmt.Errorf("image.open: %w", err)
	}
	for _, r := range l.Referrers {
		if r.ArtifactType == "" {
			return Layout{}, fmt.Errorf("image.open: %s: index.json entry %s after the image is not a referrer (no artifactType)", path, r.Digest)
		}
	}
	return l, nil
}

// Manifests lists the image's platform manifests: the image itself when it is a
// manifest, or an index's entries, each carrying its platform.
func (l Layout) Manifests(ctx context.Context) ([]ocispec.Descriptor, error) {
	if l.Descriptor.MediaType == ocispec.MediaTypeImageManifest {
		return []ocispec.Descriptor{l.Descriptor}, nil
	}
	data, err := l.readBlob(ctx, l.Descriptor)
	if err != nil {
		return nil, err
	}
	var idx ocispec.Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("%s: index %s: %w", l.Path, l.Descriptor.Digest, err)
	}
	for _, m := range idx.Manifests {
		if m.MediaType != ocispec.MediaTypeImageManifest {
			return nil, fmt.Errorf("%s: index entry %s is %q, want an OCI image manifest", l.Path, m.Digest, m.MediaType)
		}
	}
	return idx.Manifests, nil
}

// Manifest reads the image when it is one Image Manifest; an index is an error naming
// ResolvePlatform.
func (l Layout) Manifest(ctx context.Context) (ManifestLayout, error) {
	if l.Descriptor.MediaType != ocispec.MediaTypeImageManifest {
		return ManifestLayout{}, fmt.Errorf("%s holds an image index: pick a platform with ResolvePlatform", l.Path)
	}
	return l.manifest(ctx, l.Descriptor)
}

// ResolvePlatform returns the image manifest for p: the layout's own manifest when its
// config's platform matches p, or the index entry that does. Platforms compare
// normalized, so linux/arm64 finds linux/arm64/v8.
func (l Layout) ResolvePlatform(ctx context.Context, p Platform) (ManifestLayout, error) {
	manifests, err := l.Manifests(ctx)
	if err != nil {
		return ManifestLayout{}, err
	}
	if l.Descriptor.MediaType == ocispec.MediaTypeImageManifest {
		m, err := l.Manifest(ctx)
		if err != nil {
			return ManifestLayout{}, err
		}
		if m.Platform.Normalize() != p.Normalize() {
			return ManifestLayout{}, fmt.Errorf("%s holds %s, not %s", l.Path, m.Platform, p)
		}
		return m, nil
	}
	for _, d := range manifests {
		if platformFromOCI(d.Platform).Normalize() == p.Normalize() {
			return l.manifest(ctx, d)
		}
	}
	return ManifestLayout{}, fmt.Errorf("%s: no manifest for %s", l.Path, p)
}

// manifest reads the manifest desc names and its config. Layers are checked for
// presence and size only; see staging.link.
func (l Layout) manifest(ctx context.Context, desc ocispec.Descriptor) (ManifestLayout, error) {
	data, err := l.readBlob(ctx, desc)
	if err != nil {
		return ManifestLayout{}, err
	}
	var man ocispec.Manifest
	if err := json.Unmarshal(data, &man); err != nil {
		return ManifestLayout{}, fmt.Errorf("%s: manifest %s: %w", l.Path, desc.Digest, err)
	}
	if man.Config.MediaType != ocispec.MediaTypeImageConfig {
		return ManifestLayout{}, fmt.Errorf("%s: manifest %s: config is %q, want %s", l.Path, desc.Digest, man.Config.MediaType, ocispec.MediaTypeImageConfig)
	}
	cfg, err := l.config(ctx, man.Config)
	if err != nil {
		return ManifestLayout{}, err
	}
	for _, layer := range man.Layers {
		if err := checkLayerMediaType(layer.MediaType); err != nil {
			return ManifestLayout{}, fmt.Errorf("%s: manifest %s: %w", l.Path, desc.Digest, err)
		}
		info, err := os.Stat(blobPath(l.Path, layer.Digest))
		if err != nil {
			return ManifestLayout{}, fmt.Errorf("%s: layer %s: %w", l.Path, layer.Digest, err)
		}
		if info.Size() != layer.Size {
			return ManifestLayout{}, fmt.Errorf("%s: layer %s: size is %d bytes, descriptor says %d", l.Path, layer.Digest, info.Size(), layer.Size)
		}
	}
	out := ManifestLayout{
		Layout:   Layout{Path: l.Path, Descriptor: desc},
		Platform: Platform{OS: cfg.OS, Architecture: cfg.Architecture, Variant: cfg.Variant},
		Config:   man.Config,
		Layers:   man.Layers,
	}
	if desc.Digest == l.Descriptor.Digest {
		out.Layout = l
	}
	if cfg.Created != nil {
		out.Created = cfg.Created.Format(timeLayout)
	}
	return out, nil
}

func (l Layout) config(ctx context.Context, desc ocispec.Descriptor) (ocispec.Image, error) {
	data, err := l.readBlob(ctx, desc)
	if err != nil {
		return ocispec.Image{}, err
	}
	var cfg ocispec.Image
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ocispec.Image{}, fmt.Errorf("%s: config %s: %w", l.Path, desc.Digest, err)
	}
	return cfg, nil
}

// readBlob returns desc's bytes, verified against its digest and size. It is for
// manifests and configs, which fit in memory.
func (l Layout) readBlob(ctx context.Context, desc ocispec.Descriptor) ([]byte, error) {
	if err := checkDigest(desc.Digest); err != nil {
		return nil, fmt.Errorf("%s: blob %q: %w", l.Path, desc.Digest, err)
	}
	if desc.Size > registryJSONLimit {
		return nil, fmt.Errorf("%s: blob %s is %d bytes, over the %d a manifest or config may be", l.Path, desc.Digest, desc.Size, registryJSONLimit)
	}
	data, err := os.ReadFile(blobPath(l.Path, desc.Digest))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", l.Path, err)
	}
	if err := Verify(ctx, bytes.NewReader(data), desc); err != nil {
		return nil, fmt.Errorf("%s: blob %s: %w", l.Path, desc.Digest, err)
	}
	return data, nil
}

// registryJSONLimit bounds a manifest or config read into memory, as libs/oci does.
const registryJSONLimit = 4 << 20

// timeLayout renders config.created. It keeps the offset the config holds, so the
// receipt and the config bytes say the same instant the same way.
const timeLayout = "2006-01-02T15:04:05.999999999Z07:00"

func checkLayerMediaType(mt string) error {
	switch mt {
	case ocispec.MediaTypeImageLayer, ocispec.MediaTypeImageLayerGzip, ocispec.MediaTypeImageLayerZstd:
		return nil
	}
	return fmt.Errorf("layer media type %q is not an OCI layer (tar, tar+gzip, tar+zstd)", mt)
}
