package image

import (
	"context"
	"fmt"
	"os"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/oci"
)

// PullOptions configures Pull. Platform is required.
type PullOptions struct {
	// Client talks to the registry; nil pulls anonymously with a fresh oci.Client.
	Client *oci.Client
	// Platform selects the manifest when the reference names an Image Index.
	Platform Platform
	// AllowTag accepts a reference with a tag and no digest. A tag can name different
	// bytes tomorrow, so a caller that caches the result turns this on only when it
	// means to refresh.
	AllowTag bool
}

// Pull fetches ref for opts.Platform into dest as an OCI Image Layout holding that one
// Image Manifest. Every blob is fetched straight into the new layout and hashed once
// while it streams. Docker media types are refused before anything is written, and
// dest is left as it was on any error. index.json records ref (with its resolved
// digest) as org.opencontainers.image.ref.name, which Assemble writes as the base.name
// annotation of an image built over it.
func Pull(ctx context.Context, ref oci.Reference, dest string, opts PullOptions) (ManifestLayout, error) {
	if err := opts.Platform.Validate(); err != nil {
		return ManifestLayout{}, fmt.Errorf("image.pull: %w", err)
	}
	if ref.Digest == "" && !opts.AllowTag {
		return ManifestLayout{}, fmt.Errorf("image.pull: %s names a tag, which can move; pin it as name@sha256:<hex>", ref)
	}
	client := opts.Client
	if client == nil {
		client = &oci.Client{}
	}
	pinned, raw, man, err := resolveManifest(ctx, client, ref, opts.Platform)
	if err != nil {
		return ManifestLayout{}, fmt.Errorf("image.pull: %w", err)
	}
	st, err := newStaging(dest)
	if err != nil {
		return ManifestLayout{}, fmt.Errorf("image.pull: %w", err)
	}
	root, err := pullInto(ctx, st, client, pinned, raw, man, opts.Platform)
	if err == nil {
		err = st.commit(root)
	}
	if err != nil {
		st.discard()
		return ManifestLayout{}, fmt.Errorf("image.pull: %w", err)
	}
	opened, err := OpenLayout(ctx, dest)
	if err != nil {
		return ManifestLayout{}, fmt.Errorf("image.pull: %w", err)
	}
	return opened.Manifest(ctx)
}

// resolveManifest fetches ref's image manifest for p, choosing from an index, and
// refuses anything but OCI media types before a byte is written. pinned is ref with the
// digest the registry served.
func resolveManifest(ctx context.Context, client *oci.Client, ref oci.Reference, p Platform) (pinned oci.Reference, raw oci.RawManifest, man ocispec.Manifest, err error) {
	raw, err = client.Resolve(ctx, ref)
	if err != nil {
		return ref, raw, man, err
	}
	pinned = ref
	pinned.Digest = raw.Digest
	switch raw.MediaType {
	case ocispec.MediaTypeImageIndex:
		var idx ocispec.Index
		if err := json.Unmarshal(raw.Body, &idx); err != nil {
			return pinned, raw, man, fmt.Errorf("%s: index: %w", pinned, err)
		}
		chosen, err := pickPlatform(idx.Manifests, p)
		if err != nil {
			return pinned, raw, man, fmt.Errorf("%s: %w", pinned, err)
		}
		at := pinned
		at.Tag, at.Digest = "", chosen.Digest
		if raw, err = client.Manifest(ctx, at); err != nil {
			return pinned, raw, man, err
		}
	case ocispec.MediaTypeImageManifest:
	default:
		return pinned, raw, man, fmt.Errorf("%s is %q; only OCI image manifests and indexes are pulled", pinned, raw.MediaType)
	}
	if raw.MediaType != ocispec.MediaTypeImageManifest {
		return pinned, raw, man, fmt.Errorf("%s: platform entry is %q, want an OCI image manifest", pinned, raw.MediaType)
	}
	if err := json.Unmarshal(raw.Body, &man); err != nil {
		return pinned, raw, man, fmt.Errorf("%s: manifest: %w", pinned, err)
	}
	if man.Config.MediaType != ocispec.MediaTypeImageConfig {
		return pinned, raw, man, fmt.Errorf("%s: config is %q, want %s", pinned, man.Config.MediaType, ocispec.MediaTypeImageConfig)
	}
	for _, l := range man.Layers {
		if err := checkLayerMediaType(l.MediaType); err != nil {
			return pinned, raw, man, fmt.Errorf("%s: %w", pinned, err)
		}
	}
	return pinned, raw, man, nil
}

func pullInto(ctx context.Context, st *staging, client *oci.Client, ref oci.Reference, raw oci.RawManifest, man ocispec.Manifest, want Platform) (ocispec.Descriptor, error) {
	root, err := st.put(raw.MediaType, raw.Body)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(transferConcurrency)
	for _, d := range append([]ocispec.Descriptor{man.Config}, man.Layers...) {
		g.Go(func() error { return st.fetch(gctx, client, ref, d) })
	}
	if err := g.Wait(); err != nil {
		return ocispec.Descriptor{}, err
	}
	root.Annotations = map[string]string{ocispec.AnnotationRefName: ref.String()}
	data, err := os.ReadFile(st.path(man.Config.Digest))
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	var cfg ocispec.Image
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("config %s: %w", man.Config.Digest, err)
	}
	got := Platform{OS: cfg.OS, Architecture: cfg.Architecture, Variant: cfg.Variant}
	if got.Normalize() != want.Normalize() {
		return ocispec.Descriptor{}, fmt.Errorf("%s: config says %s, not %s", ref, got, want)
	}
	root.Platform = got.oci()
	return root, nil
}

func pickPlatform(manifests []ocispec.Descriptor, want Platform) (ocispec.Descriptor, error) {
	for _, m := range manifests {
		if m.Platform != nil && platformOf(m.Platform).Normalize() == want.Normalize() {
			return m, nil
		}
	}
	return ocispec.Descriptor{}, fmt.Errorf("no manifest for %s", want)
}

// PushOptions configures Push.
type PushOptions struct {
	// Client talks to the registry; nil pushes anonymously with a fresh oci.Client.
	Client *oci.Client
	// Tags the image is written under after its digest. Empty pushes by digest only.
	Tags []string
}

// Pushed is what a registry holds after Push.
type Pushed struct {
	Repository oci.Reference
	Digest     digest.Digest
	Tags       []string
}

// Reference is Repository pinned at Digest: what a signer or a deploy consumes.
func (p Pushed) Reference() oci.Reference {
	r := p.Repository
	r.Tag, r.Digest = "", p.Digest
	return r
}

// Push uploads the layout at path to repo's repository: every blob the registry lacks,
// each child manifest, the image under its digest and each tag, then each referrer.
// A blob the registry holds costs one HEAD; one it lacks is hashed once, while it
// uploads, and a local blob that does not match its descriptor aborts the upload.
func Push(ctx context.Context, path string, repo oci.Reference, opts PushOptions) (Pushed, error) {
	for _, t := range opts.Tags {
		if err := oci.ValidateTag(t); err != nil {
			return Pushed{}, fmt.Errorf("image.push: %w", err)
		}
	}
	l, err := OpenLayout(ctx, path)
	if err != nil {
		return Pushed{}, fmt.Errorf("image.push: %w", err)
	}
	client := opts.Client
	if client == nil {
		client = &oci.Client{}
	}
	repo.Tag, repo.Digest = "", ""
	if err := pushLayout(ctx, client, l, repo, opts.Tags); err != nil {
		return Pushed{}, fmt.Errorf("image.push: %w", err)
	}
	return Pushed{Repository: repo, Digest: l.Descriptor.Digest, Tags: append([]string(nil), opts.Tags...)}, nil
}

// layoutBlobs lists the config and layers of every manifest l reaches, and every
// manifest but the image itself, without reading a blob it does not have to.
func layoutBlobs(ctx context.Context, l Layout) (blobs, manifests []ocispec.Descriptor, err error) {
	platforms, err := l.Manifests(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, m := range append(platforms, l.Referrers...) {
		data, err := l.readBlob(ctx, m)
		if err != nil {
			return nil, nil, err
		}
		var man ocispec.Manifest
		if err := json.Unmarshal(data, &man); err != nil {
			return nil, nil, fmt.Errorf("manifest %s: %w", m.Digest, err)
		}
		blobs = append(append(blobs, man.Config), man.Layers...)
		if m.Digest != l.Descriptor.Digest {
			manifests = append(manifests, m)
		}
	}
	return blobs, manifests, nil
}

func pushLayout(ctx context.Context, client *oci.Client, l Layout, repo oci.Reference, tags []string) error {
	blobs, bodies, err := layoutBlobs(ctx, l)
	if err != nil {
		return err
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(transferConcurrency)
	seen := map[digest.Digest]bool{}
	for _, b := range blobs {
		if seen[b.Digest] {
			continue
		}
		seen[b.Digest] = true
		g.Go(func() error { return pushBlob(gctx, client, repo, l.Path, b) })
	}
	if err := g.Wait(); err != nil {
		return err
	}
	referrers := map[digest.Digest]bool{}
	for _, r := range l.Referrers {
		referrers[r.Digest] = true
	}
	var after []ocispec.Descriptor
	for _, m := range bodies {
		if referrers[m.Digest] {
			after = append(after, m)
			continue
		}
		if err := putManifest(ctx, client, repo, l, m); err != nil {
			return err
		}
	}
	if err := putManifest(ctx, client, repo, l, l.Descriptor, tags...); err != nil {
		return err
	}
	for _, m := range after {
		if err := putManifest(ctx, client, repo, l, m); err != nil {
			return err
		}
	}
	return nil
}

func pushBlob(ctx context.Context, client *oci.Client, repo oci.Reference, layout string, d ocispec.Descriptor) error {
	if err := checkDigest(d.Digest); err != nil {
		return fmt.Errorf("blob %q: %w", d.Digest, err)
	}
	f, err := os.Open(blobPath(layout, d.Digest))
	if err != nil {
		return err
	}
	defer f.Close()
	if err := client.PushBlob(ctx, repo, d, f); err != nil {
		return fmt.Errorf("blob %s: %w", d.Digest, err)
	}
	return nil
}

func putManifest(ctx context.Context, client *oci.Client, repo oci.Reference, l Layout, m ocispec.Descriptor, tags ...string) error {
	body, err := l.readBlob(ctx, m)
	if err != nil {
		return err
	}
	if _, err := client.PutManifest(ctx, repo, m.MediaType, body, tags...); err != nil {
		return fmt.Errorf("manifest %s: %w", m.Digest, err)
	}
	return nil
}
