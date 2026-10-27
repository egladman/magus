package image

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/libs/oci"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
)

// PullLayout fetches a digest-pinned image for platform into dest as an OCI layout.
// An Image Index reference selects the matching platform manifest; the layout
// holds that single Image Manifest.
func PullLayout(ctx context.Context, reference string, platform Platform, dest string, auth *RegistryAuth) (Layout, error) {
	if err := platform.Validate(); err != nil {
		return Layout{}, fmt.Errorf("image.pull: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Layout{}, fmt.Errorf("image.pull: %w", err)
	}
	ref, err := oci.ParseReference(reference)
	if err != nil {
		return Layout{}, fmt.Errorf("image.pull: %w", err)
	}
	if ref.Digest == "" {
		return Layout{}, fmt.Errorf("image.pull: reference %q: want name@sha256:<hex>, not a tag", reference)
	}
	client := registryClient(auth)
	repo := fmt.Sprintf("%s/%s", ref.Registry, ref.Repository)

	raw, err := client.Manifest(ctx, ref)
	if err != nil {
		return Layout{}, fmt.Errorf("image.pull: %w", err)
	}

	switch mediaTypeKind(raw.MediaType) {
	case "index":
		var idx ocispec.Index
		if err := json.Unmarshal(raw.Body, &idx); err != nil {
			return Layout{}, fmt.Errorf("image.pull: decode index: %w", err)
		}
		chosen, err := pickPlatform(idx.Manifests, platform)
		if err != nil {
			return Layout{}, fmt.Errorf("image.pull: %w", err)
		}
		manRef := ref
		manRef.Digest = chosen.Digest
		manRef.Tag = ""
		raw, err = client.Manifest(ctx, manRef)
		if err != nil {
			return Layout{}, fmt.Errorf("image.pull: platform manifest: %w", err)
		}
	case "manifest":
	default:
		return Layout{}, fmt.Errorf("image.pull: unsupported media type %q", raw.MediaType)
	}

	blobs, err := materializeManifest(ctx, client, repo, raw)
	if err != nil {
		return Layout{}, err
	}
	defer removeBlobTemps(blobs)
	root := raw.Descriptor
	if err := writeLayout(ctx, dest, root, blobs); err != nil {
		return Layout{}, fmt.Errorf("image.pull: %w", err)
	}
	out := &Layout{
		Path:       dest,
		Descriptor: root,
		Platform:   platform,
		Op:         OpPullLayout,
		Base:       reference,
		BaseDigest: MustDigest(ref.Digest),
	}
	input, err := readPlatformLayout(ctx, dest)
	if err != nil {
		return Layout{}, fmt.Errorf("image.pull: verify layout: %w", err)
	}
	setCreated(out, input.config.Created)
	for _, b := range input.blobs {
		if b.desc.MediaType == ocispec.MediaTypeImageConfig {
			out.ConfigDigest = MustDigest(b.desc.Digest)
			break
		}
	}
	return *out, nil
}

// PublishLayout pushes a layout to repository under tags. Empty tags means digest-only.
func PublishLayout(ctx context.Context, layoutPath, repository string, tags []string, auth *RegistryAuth) (Published, error) {
	if err := ctx.Err(); err != nil {
		return Published{}, fmt.Errorf("image.publish: %w", err)
	}
	if os.Getenv("MAGUS_OFFLINE") != "" {
		return Published{}, fmt.Errorf("image.publish: MAGUS_OFFLINE is set")
	}
	layout, err := OpenLayout(layoutPath)
	if err != nil {
		return Published{}, fmt.Errorf("image.publish: %w", err)
	}
	if _, err := oci.ParseRepository(repository); err != nil {
		return Published{}, fmt.Errorf("image.publish: %w", err)
	}
	client := registryClient(auth)
	blobs, err := collectLayoutBlobs(ctx, layout)
	if err != nil {
		return Published{}, fmt.Errorf("image.publish: %w", err)
	}
	seen := map[digest.Digest]bool{}
	var manifests []blob
	var layers []blob
	for _, b := range blobs {
		if seen[b.desc.Digest] {
			continue
		}
		seen[b.desc.Digest] = true
		kind := mediaTypeKind(b.desc.MediaType)
		if kind == "index" || kind == "manifest" {
			if b.desc.Digest != layout.Descriptor.Digest {
				manifests = append(manifests, b)
			}
			continue
		}
		layers = append(layers, b)
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(4, runtime.GOMAXPROCS(0)))
	for _, b := range layers {
		g.Go(func() error {
			r, err := b.open()
			if err != nil {
				return fmt.Errorf("image.publish: open %s: %w", b.desc.Digest, err)
			}
			err = client.PushBlob(gctx, repository, b.desc, r)
			_ = r.Close()
			if err != nil {
				return fmt.Errorf("image.publish: blob %s: %w", b.desc.Digest, err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return Published{}, err
	}
	for _, m := range manifests {
		body, err := readAllBlob(m)
		if err != nil {
			return Published{}, fmt.Errorf("image.publish: nested manifest %s: %w", m.desc.Digest, err)
		}
		if _, err := client.PutManifest(ctx, repository, m.desc.MediaType, body); err != nil {
			return Published{}, fmt.Errorf("image.publish: nested manifest %s: %w", m.desc.Digest, err)
		}
	}
	rootBody, err := readBlob(layout.Path, layout.Descriptor)
	if err != nil {
		return Published{}, fmt.Errorf("image.publish: root: %w", err)
	}
	confirmed, err := client.PutManifest(ctx, repository, layout.Descriptor.MediaType, rootBody, tags...)
	if err != nil {
		return Published{}, fmt.Errorf("image.publish: %w", err)
	}
	return Published{
		Repository: repository,
		Digest:     MustDigest(confirmed),
		Tags:       append([]string(nil), tags...),
		Op:         OpPublishLayout,
	}, nil
}

func readAllBlob(b blob) ([]byte, error) {
	r, err := b.open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func registryClient(auth *RegistryAuth) *oci.Client {
	client := &oci.Client{}
	if auth != nil && (auth.Username != "" || auth.Password.Value != "") {
		client.Credentials = oci.Credentials{
			Username: auth.Username,
			Password: secret.NewValue(auth.Password.Value),
		}
	}
	return client
}

func pickPlatform(manifests []ocispec.Descriptor, want Platform) (ocispec.Descriptor, error) {
	for _, m := range manifests {
		p := m.Platform
		if p == nil {
			continue
		}
		if p.OS == want.OS && p.Architecture == want.Architecture && p.Variant == want.Variant {
			return m, nil
		}
	}
	return ocispec.Descriptor{}, fmt.Errorf("no manifest for %s/%s", want.OS, want.Architecture)
}

func materializeManifest(ctx context.Context, client *oci.Client, repo string, raw oci.RawManifest) ([]blob, error) {
	var man ocispec.Manifest
	if err := json.Unmarshal(raw.Body, &man); err != nil {
		return nil, fmt.Errorf("image.pull: decode manifest: %w", err)
	}
	descs := append([]ocispec.Descriptor{man.Config}, man.Layers...)
	out := make([]blob, len(descs)+1)
	out[0] = bytesBlob(raw.MediaType, raw.Body)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(4, runtime.GOMAXPROCS(0)))
	for i, d := range descs {
		g.Go(func() error {
			b, err := fetchRegistryBlob(gctx, client, repo, d)
			if err != nil {
				return err
			}
			out[i+1] = b
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		removeBlobTemps(out)
		return nil, err
	}
	return out, nil
}

func removeBlobTemps(blobs []blob) {
	for _, b := range blobs {
		if b.removeAfter && b.path != "" {
			_ = os.Remove(b.path)
		}
	}
}

func fetchRegistryBlob(ctx context.Context, client *oci.Client, repo string, d ocispec.Descriptor) (blob, error) {
	if err := ctx.Err(); err != nil {
		return blob{}, fmt.Errorf("image.pull: %w", err)
	}
	f, err := os.CreateTemp("", "magus-image-blob-*")
	if err != nil {
		return blob{}, fmt.Errorf("image.pull: temp blob: %w", err)
	}
	tmp := f.Name()
	if err := client.FetchBlob(ctx, repo, d, f); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return blob{}, fmt.Errorf("image.pull: blob %s: %w", d.Digest, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return blob{}, fmt.Errorf("image.pull: blob %s: %w", d.Digest, err)
	}
	return blob{
		desc:        d,
		path:        tmp,
		removeAfter: true,
		open:        func() (io.ReadCloser, error) { return os.Open(tmp) },
	}, nil
}

func collectLayoutBlobs(ctx context.Context, layout Layout) ([]blob, error) {
	rootData, err := readBlob(layout.Path, layout.Descriptor)
	if err != nil {
		return nil, err
	}
	blobs := []blob{bytesBlob(layout.Descriptor.MediaType, rootData)}
	switch mediaTypeKind(layout.Descriptor.MediaType) {
	case "index":
		var idx ocispec.Index
		if err := json.Unmarshal(rootData, &idx); err != nil {
			return nil, fmt.Errorf("decode index: %w", err)
		}
		for _, m := range idx.Manifests {
			manData, err := readBlob(layout.Path, m)
			if err != nil {
				return nil, fmt.Errorf("manifest %s: %w", m.Digest, err)
			}
			blobs = append(blobs, bytesBlob(m.MediaType, manData))
			more, err := blobsFromManifest(ctx, layout.Path, manData)
			if err != nil {
				return nil, err
			}
			blobs = append(blobs, more...)
		}
	case "manifest":
		more, err := blobsFromManifest(ctx, layout.Path, rootData)
		if err != nil {
			return nil, err
		}
		blobs = append(blobs, more...)
	default:
		return nil, fmt.Errorf("unsupported media type %q", layout.Descriptor.MediaType)
	}
	return blobs, nil
}

func blobsFromManifest(ctx context.Context, path string, manData []byte) ([]blob, error) {
	var man ocispec.Manifest
	if err := json.Unmarshal(manData, &man); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	descs := append([]ocispec.Descriptor{man.Config}, man.Layers...)
	out := make([]blob, len(descs))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(4, runtime.GOMAXPROCS(0)))
	for i, d := range descs {
		g.Go(func() error {
			if err := verifyLayoutBlob(gctx, path, d); err != nil {
				return err
			}
			out[i] = layoutBlob(path, d)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

func mediaTypeKind(mt string) string {
	switch {
	case mt == ocispec.MediaTypeImageIndex || strings.Contains(mt, "manifest.list"):
		return "index"
	case mt == ocispec.MediaTypeImageManifest || strings.Contains(mt, "manifest.v2"):
		return "manifest"
	default:
		return mt
	}
}
