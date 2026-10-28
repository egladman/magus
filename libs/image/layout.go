package image

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/oci"
)

// Docker's image media types. image-spec's compatibility matrix calls them
// interchangeable with the OCI ones, so a layout or registry holding them is read and
// copied as is; only Assemble converts them, and only over a base.
const (
	mediaTypeDockerManifest     = "application/vnd.docker.distribution.manifest.v2+json"
	mediaTypeDockerManifestList = "application/vnd.docker.distribution.manifest.list.v2+json"
	mediaTypeDockerConfig       = "application/vnd.docker.container.image.v1+json"
	mediaTypeDockerLayerGzip    = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	mediaTypeDockerForeignLayer = "application/vnd.docker.image.rootfs.foreign.diff.tar.gzip"
)

func isImageManifest(mt string) bool {
	return mt == ocispec.MediaTypeImageManifest || mt == mediaTypeDockerManifest
}

func isImageIndex(mt string) bool {
	return mt == ocispec.MediaTypeImageIndex || mt == mediaTypeDockerManifestList
}

func isImageConfig(mt string) bool {
	return mt == ocispec.MediaTypeImageConfig || mt == mediaTypeDockerConfig
}

// isNonDistributable reports a layer a registry need not hold (image-spec layer.md);
// Pull and Push leave its blob out and Assemble refuses it. The OCI types are spelled
// here because image-spec deprecates its constants for writers, and a reader must still
// know them.
func isNonDistributable(mt string) bool {
	switch mt {
	case "application/vnd.oci.image.layer.nondistributable.v1.tar",
		"application/vnd.oci.image.layer.nondistributable.v1.tar+gzip",
		"application/vnd.oci.image.layer.nondistributable.v1.tar+zstd",
		mediaTypeDockerForeignLayer:
		return true
	}
	return false
}

// Layout is an OCI Image Layout on disk, opened at one image: an index.json entry that is
// an image manifest or index with no subject. Referrers are the index.json entries whose
// manifest names that image as its subject.
type Layout struct {
	Path       string
	Descriptor ocispec.Descriptor
	Referrers  []ocispec.Descriptor
}

// Manifest is one Image Manifest of a layout, with the facts of the manifest and its
// config.
type Manifest struct {
	Layout
	Platform Platform
	Config   ocispec.Descriptor
	Layers   []ocispec.Descriptor
	// Added is the layer Assemble wrote, nil when it wrote none or the layout was not
	// assembled.
	Added *Layer
	// Created is the config's created time as RFC 3339, empty when the config omits it.
	Created string
}

// OpenLayout reads the layout at path and opens the image named name: the index.json
// entry whose org.opencontainers.image.ref.name is name. An empty name opens the
// layout's only image and is an error when it holds several. Entries of a media type
// this package does not read are passed over, never an error. The image's root blob is
// verified against its descriptor. A missing layout wraps fs.ErrNotExist.
func OpenLayout(ctx context.Context, path, name string) (Layout, error) {
	if err := ctx.Err(); err != nil {
		return Layout{}, err
	}
	idx, err := readIndexJSON(path)
	if err != nil {
		return Layout{}, fmt.Errorf("image.open: %w", err)
	}
	l := Layout{Path: path}
	var images []ocispec.Descriptor
	subjects := map[digest.Digest]digest.Digest{}
	for _, d := range idx.Manifests {
		if !isImageManifest(d.MediaType) && !isImageIndex(d.MediaType) {
			continue
		}
		subject, err := l.subject(ctx, d)
		if err != nil {
			return Layout{}, fmt.Errorf("image.open: %w", err)
		}
		if subject == "" {
			images = append(images, d)
		} else {
			subjects[d.Digest] = subject
		}
	}
	l.Descriptor, err = selectImage(path, images, name)
	if err != nil {
		return Layout{}, fmt.Errorf("image.open: %w", err)
	}
	for _, d := range idx.Manifests {
		if subjects[d.Digest] == l.Descriptor.Digest {
			l.Referrers = append(l.Referrers, d)
		}
	}
	return l, nil
}

func readIndexJSON(path string) (ocispec.Index, error) {
	marker, err := os.ReadFile(filepath.Join(path, "oci-layout"))
	if err != nil {
		return ocispec.Index{}, err
	}
	var m ocispec.ImageLayout
	if err := json.Unmarshal(marker, &m); err != nil {
		return ocispec.Index{}, fmt.Errorf("%s: oci-layout: %w", path, err)
	}
	if m.Version != layoutVersion {
		return ocispec.Index{}, fmt.Errorf("%s: oci-layout version %q, want %q", path, m.Version, layoutVersion)
	}
	raw, err := os.ReadFile(filepath.Join(path, "index.json"))
	if err != nil {
		return ocispec.Index{}, err
	}
	var idx ocispec.Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return ocispec.Index{}, fmt.Errorf("%s: index.json: %w", path, err)
	}
	return idx, nil
}

// subject reads d's blob, verifying it, and returns the digest its subject names, empty
// when it has none.
func (l Layout) subject(ctx context.Context, d ocispec.Descriptor) (digest.Digest, error) {
	data, err := l.readBlob(ctx, d)
	if err != nil {
		return "", err
	}
	var m struct {
		Subject *ocispec.Descriptor `json:"subject"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return "", fmt.Errorf("%s: manifest %s: %w", l.Path, d.Digest, err)
	}
	if m.Subject == nil {
		return "", nil
	}
	return m.Subject.Digest, nil
}

func selectImage(path string, images []ocispec.Descriptor, name string) (ocispec.Descriptor, error) {
	names := make([]string, 0, len(images))
	var matched []ocispec.Descriptor
	for _, d := range images {
		n := d.Annotations[ocispec.AnnotationRefName]
		names = append(names, fmt.Sprintf("%q", n))
		if name == "" || n == name {
			matched = append(matched, d)
		}
	}
	switch {
	case len(matched) == 1:
		return matched[0], nil
	case len(images) == 0:
		return ocispec.Descriptor{}, fmt.Errorf("%s: index.json names no image manifest or index", path)
	case name == "":
		return ocispec.Descriptor{}, fmt.Errorf("%s holds %d images (ref.name %s): name one", path, len(images), strings.Join(names, ", "))
	case len(matched) == 0:
		return ocispec.Descriptor{}, fmt.Errorf("%s: no image has ref.name %q, it holds %s", path, name, strings.Join(names, ", "))
	}
	return ocispec.Descriptor{}, fmt.Errorf("%s: %d images have ref.name %q", path, len(matched), name)
}

// Manifests lists the image's platform manifests: the image itself when it is a
// manifest, or the image manifests an index lists, each carrying its platform. An index
// entry of another media type, a nested index included, is passed over.
func (l Layout) Manifests(ctx context.Context) ([]ocispec.Descriptor, error) {
	if isImageManifest(l.Descriptor.MediaType) {
		return []ocispec.Descriptor{l.Descriptor}, nil
	}
	idx, err := l.index(ctx, l.Descriptor)
	if err != nil {
		return nil, err
	}
	var out []ocispec.Descriptor
	for _, m := range idx.Manifests {
		if isImageManifest(m.MediaType) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (l Layout) index(ctx context.Context, d ocispec.Descriptor) (ocispec.Index, error) {
	data, err := l.readBlob(ctx, d)
	if err != nil {
		return ocispec.Index{}, err
	}
	var idx ocispec.Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return ocispec.Index{}, fmt.Errorf("%s: index %s: %w", l.Path, d.Digest, err)
	}
	return idx, nil
}

// Manifest returns the image manifest for p: the layout's own manifest when its config's
// platform matches p, or the index entry that does. Platforms compare normalized, so
// linux/arm64 finds linux/arm64/v8.
func (l Layout) Manifest(ctx context.Context, p Platform) (Manifest, error) {
	if isImageManifest(l.Descriptor.MediaType) {
		m, err := l.manifest(ctx, l.Descriptor)
		if err != nil {
			return Manifest{}, err
		}
		if !m.Platform.Matches(p) {
			return Manifest{}, fmt.Errorf("%s holds %s, not %s", l.Path, m.Platform, p)
		}
		return m, nil
	}
	manifests, err := l.Manifests(ctx)
	if err != nil {
		return Manifest{}, err
	}
	for _, d := range manifests {
		if platformFromOCI(d.Platform).Matches(p) {
			return l.manifest(ctx, d)
		}
	}
	return Manifest{}, fmt.Errorf("%s: no manifest for %s", l.Path, p)
}

// manifest reads the manifest desc names and its config. Layers are checked for
// presence and size only (see staging.link); a non-distributable layer need not be
// present.
func (l Layout) manifest(ctx context.Context, desc ocispec.Descriptor) (Manifest, error) {
	man, err := l.imageManifest(ctx, desc)
	if err != nil {
		return Manifest{}, err
	}
	cfg, _, err := l.config(ctx, man.Config)
	if err != nil {
		return Manifest{}, err
	}
	for _, layer := range man.Layers {
		if isNonDistributable(layer.MediaType) {
			continue
		}
		if err := checkDigest(layer.Digest); err != nil {
			return Manifest{}, fmt.Errorf("%s: layer %q: %w", l.Path, layer.Digest, err)
		}
		info, err := os.Stat(blobPath(l.Path, layer.Digest))
		if err != nil {
			return Manifest{}, fmt.Errorf("%s: layer %s: %w", l.Path, layer.Digest, err)
		}
		if info.Size() != layer.Size {
			return Manifest{}, fmt.Errorf("%s: layer %s: size is %d bytes, descriptor says %d", l.Path, layer.Digest, info.Size(), layer.Size)
		}
	}
	out := Manifest{
		Layout:   Layout{Path: l.Path, Descriptor: desc},
		Platform: platformFromConfig(cfg),
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

// imageManifest reads an OCI or Docker image manifest whose config is an image config.
func (l Layout) imageManifest(ctx context.Context, desc ocispec.Descriptor) (ocispec.Manifest, error) {
	if !isImageManifest(desc.MediaType) {
		return ocispec.Manifest{}, fmt.Errorf("%s: %s is %q, want an image manifest", l.Path, desc.Digest, desc.MediaType)
	}
	data, err := l.readBlob(ctx, desc)
	if err != nil {
		return ocispec.Manifest{}, err
	}
	var man ocispec.Manifest
	if err := json.Unmarshal(data, &man); err != nil {
		return ocispec.Manifest{}, fmt.Errorf("%s: manifest %s: %w", l.Path, desc.Digest, err)
	}
	if !isImageConfig(man.Config.MediaType) {
		return ocispec.Manifest{}, fmt.Errorf("%s: manifest %s: config is %q, want %s", l.Path, desc.Digest, man.Config.MediaType, ocispec.MediaTypeImageConfig)
	}
	return man, nil
}

// config reads an image config, returning it decoded and as the bytes on disk, which
// keep the members ocispec.Image does not model.
func (l Layout) config(ctx context.Context, desc ocispec.Descriptor) (ocispec.Image, []byte, error) {
	data, err := l.readBlob(ctx, desc)
	if err != nil {
		return ocispec.Image{}, nil, err
	}
	var cfg ocispec.Image
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ocispec.Image{}, nil, fmt.Errorf("%s: config %s: %w", l.Path, desc.Digest, err)
	}
	return cfg, data, nil
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
	if err := oci.VerifyBlob(ctx, bytes.NewReader(data), desc); err != nil {
		return nil, fmt.Errorf("%s: blob %s: %w", l.Path, desc.Digest, err)
	}
	return data, nil
}

// walk calls visit for every descriptor the image reaches and then each referrer:
// manifests and indexes before what they list, configs and layers after their
// manifest. A nested index is walked; an index entry of a media type this package does
// not read is visited and not opened. manifest is true for a manifest or index entry
// and false for a config or layer.
func (l Layout) walk(ctx context.Context, visit func(d ocispec.Descriptor, manifest bool) error) error {
	var step func(d ocispec.Descriptor) error
	step = func(d ocispec.Descriptor) error {
		if err := visit(d, true); err != nil {
			return err
		}
		switch {
		case isImageIndex(d.MediaType):
			idx, err := l.index(ctx, d)
			if err != nil {
				return err
			}
			for _, m := range idx.Manifests {
				if err := step(m); err != nil {
					return err
				}
			}
		case isImageManifest(d.MediaType):
			data, err := l.readBlob(ctx, d)
			if err != nil {
				return err
			}
			var man ocispec.Manifest
			if err := json.Unmarshal(data, &man); err != nil {
				return fmt.Errorf("manifest %s: %w", d.Digest, err)
			}
			for _, b := range slices.Concat([]ocispec.Descriptor{man.Config}, man.Layers) {
				if err := visit(b, false); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, d := range slices.Concat([]ocispec.Descriptor{l.Descriptor}, l.Referrers) {
		if err := step(d); err != nil {
			return err
		}
	}
	return nil
}

// registryJSONLimit bounds a manifest or config read into memory, as libs/oci does.
const registryJSONLimit = 4 << 20

// timeLayout renders config.created. It keeps the offset the config holds, so the
// receipt and the config bytes say the same instant the same way.
const timeLayout = "2006-01-02T15:04:05.999999999Z07:00"
