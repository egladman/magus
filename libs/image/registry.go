package image

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/oci"
)

// Pull fetches the image ref pins, for platform p, into dest as a layout holding that
// one image manifest. ref must carry a digest; resolve a tag first (oci.Client.Resolve).
// Each blob is hashed once while it streams, and dest is unchanged on any error. Media
// types are kept as served, Docker's included; a non-distributable layer is described,
// not fetched. index.json records ref pinned at the platform manifest as
// org.opencontainers.image.ref.name, which Assemble writes as base.name.
func Pull(ctx context.Context, c *oci.Client, ref oci.Reference, p Platform, dest string) (Manifest, error) {
	if c == nil {
		return Manifest{}, errors.New("image.pull: no oci.Client, &oci.Client{} pulls anonymously")
	}
	if err := p.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("image.pull: %w", err)
	}
	if ref.Digest == "" {
		return Manifest{}, fmt.Errorf("image.pull: %s names a tag, which can move: resolve it, then pull name@sha256:<hex>", ref)
	}
	pinned, raw, man, err := resolveManifest(ctx, c, ref, p)
	if err != nil {
		return Manifest{}, fmt.Errorf("image.pull: %w", err)
	}
	st, err := newStaging(dest)
	if err != nil {
		return Manifest{}, fmt.Errorf("image.pull: %w", err)
	}
	root, err := pullInto(ctx, st, c, pinned, raw, man, p)
	if err == nil {
		err = st.commit(root)
	}
	if err != nil {
		st.discard()
		return Manifest{}, fmt.Errorf("image.pull: %w", err)
	}
	opened, err := OpenLayout(ctx, dest, "")
	if err != nil {
		return Manifest{}, fmt.Errorf("image.pull: %w", err)
	}
	return opened.Manifest(ctx, p)
}

// resolveManifest fetches ref's image manifest for p, choosing from an index. pinned is
// the repository at the digest of the manifest chosen.
func resolveManifest(ctx context.Context, c *oci.Client, ref oci.Reference, p Platform) (pinned oci.Reference, raw oci.RawManifest, man ocispec.Manifest, err error) {
	raw, err = c.GetManifest(ctx, ref)
	if err != nil {
		return ref, raw, man, err
	}
	if isImageIndex(raw.MediaType) {
		var idx ocispec.Index
		if err := json.Unmarshal(raw.Body, &idx); err != nil {
			return ref, raw, man, fmt.Errorf("%s: index: %w", ref, err)
		}
		chosen, err := pickPlatform(idx.Manifests, p)
		if err != nil {
			return ref, raw, man, fmt.Errorf("%s: %w", ref, err)
		}
		at := ref
		at.Tag, at.Digest = "", chosen.Digest
		if raw, err = c.GetManifest(ctx, at); err != nil {
			return ref, raw, man, err
		}
	}
	pinned = ref
	pinned.Tag, pinned.Digest = "", raw.Digest
	if !isImageManifest(raw.MediaType) {
		return pinned, raw, man, fmt.Errorf("%s is %q, want an image manifest or index", pinned, raw.MediaType)
	}
	if err := json.Unmarshal(raw.Body, &man); err != nil {
		return pinned, raw, man, fmt.Errorf("%s: manifest: %w", pinned, err)
	}
	return pinned, raw, man, nil
}

func pullInto(ctx context.Context, st *staging, c *oci.Client, ref oci.Reference, raw oci.RawManifest, man ocispec.Manifest, want Platform) (ocispec.Descriptor, error) {
	root, err := st.put(raw.MediaType, raw.Body)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(transferConcurrency)
	for _, d := range append([]ocispec.Descriptor{man.Config}, man.Layers...) {
		if isNonDistributable(d.MediaType) {
			continue
		}
		g.Go(func() error { return st.fetch(gctx, c, ref, d) })
	}
	if err := g.Wait(); err != nil {
		return ocispec.Descriptor{}, err
	}
	root.Annotations = map[string]string{ocispec.AnnotationRefName: ref.String()}
	if !isImageConfig(man.Config.MediaType) {
		return root, nil
	}
	data, err := os.ReadFile(st.path(man.Config.Digest))
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	var cfg ocispec.Image
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("config %s: %w", man.Config.Digest, err)
	}
	got := platformFromConfig(cfg)
	if !got.Matches(want) {
		return ocispec.Descriptor{}, fmt.Errorf("%s: config says %s, not %s", ref, got, want)
	}
	root.Platform = got.oci()
	return root, nil
}

func pickPlatform(manifests []ocispec.Descriptor, want Platform) (ocispec.Descriptor, error) {
	for _, m := range manifests {
		if m.Platform != nil && platformFromOCI(m.Platform).Matches(want) {
			return m, nil
		}
	}
	return ocispec.Descriptor{}, fmt.Errorf("no manifest for %s", want)
}

// Push uploads the layout's image to repo's repository and returns repo pinned at the
// image's digest. It sends every blob the registry lacks, each manifest an index lists,
// the image under its digest and then each of tags, then each referrer, a platform's
// included (see oci.Client.PushManifest for registries without the Referrers API). A
// blob of an image whose base.name is another repository on repo's registry is mounted
// from there (oci.Client.MountBlob). A blob is hashed once, while it uploads, and one
// that does not match its descriptor aborts the upload. A non-distributable layer is
// not uploaded, as image-spec asks.
func Push(ctx context.Context, c *oci.Client, layout string, repo oci.Reference, tags ...string) (oci.Reference, error) {
	if c == nil {
		return oci.Reference{}, errors.New("image.push: no oci.Client, &oci.Client{} pushes anonymously")
	}
	for _, t := range tags {
		if err := oci.ValidateTag(t); err != nil {
			return oci.Reference{}, fmt.Errorf("image.push: %w", err)
		}
	}
	l, err := OpenLayout(ctx, layout, "")
	if err == nil {
		l, err = withPlatformReferrers(ctx, l)
	}
	if err != nil {
		return oci.Reference{}, fmt.Errorf("image.push: %w", err)
	}
	repo.Tag, repo.Digest = "", ""
	if err := pushLayout(ctx, c, l, repo, tags); err != nil {
		return oci.Reference{}, fmt.Errorf("image.push: %w", err)
	}
	repo.Digest = l.Descriptor.Digest
	return repo, nil
}

// withPlatformReferrers adds to l.Referrers the layout's referrers whose subject is a
// manifest l's index lists: the per-platform ones AssembleIndex keeps.
func withPlatformReferrers(ctx context.Context, l Layout) (Layout, error) {
	if !isImageIndex(l.Descriptor.MediaType) {
		return l, nil
	}
	listed, err := l.index(ctx, l.Descriptor)
	if err != nil {
		return l, err
	}
	platforms := map[digest.Digest]bool{}
	for _, m := range listed.Manifests {
		platforms[m.Digest] = true
	}
	idx, err := readIndexJSON(l.Path)
	if err != nil {
		return l, err
	}
	for _, d := range idx.Manifests {
		if !isImageManifest(d.MediaType) {
			continue
		}
		subject, err := l.subject(ctx, d)
		if err != nil {
			return l, err
		}
		if platforms[subject] {
			l.Referrers = append(l.Referrers, d)
		}
	}
	return l, nil
}

// mountSources maps each blob of l's image manifests to the repository a cross-repository
// mount can take it from: the base its manifest names in base.name, when that is another
// repository on repo's registry. A base.name that does not parse names no such repository.
// A blob the base does not hold is uploaded as usual, so every blob of the manifest asks.
func mountSources(ctx context.Context, l Layout, repo oci.Reference) (map[digest.Digest]oci.Reference, error) {
	images := []ocispec.Descriptor{l.Descriptor}
	if isImageIndex(l.Descriptor.MediaType) {
		listed, err := l.index(ctx, l.Descriptor)
		if err != nil {
			return nil, err
		}
		images = listed.Manifests
	}
	sources := map[digest.Digest]oci.Reference{}
	for _, d := range images {
		if !isImageManifest(d.MediaType) {
			continue
		}
		data, err := l.readBlob(ctx, d)
		if err != nil {
			return nil, err
		}
		var man ocispec.Manifest
		if err := json.Unmarshal(data, &man); err != nil {
			return nil, fmt.Errorf("manifest %s: %w", d.Digest, err)
		}
		base, err := oci.ParseReference(man.Annotations[ocispec.AnnotationBaseImageName])
		if err != nil || base.Registry != repo.Registry || base.Repository == repo.Repository {
			continue
		}
		for _, b := range append([]ocispec.Descriptor{man.Config}, man.Layers...) {
			sources[b.Digest] = base
		}
	}
	return sources, nil
}

// layoutContents sorts what l reaches into blobs and the manifests to push before the
// image, in an order where each manifest follows everything it lists.
func layoutContents(ctx context.Context, l Layout) (blobs, manifests []ocispec.Descriptor, err error) {
	referrers := map[digest.Digest]bool{}
	for _, r := range l.Referrers {
		referrers[r.Digest] = true
	}
	var stack []ocispec.Descriptor
	err = l.walk(ctx, func(d ocispec.Descriptor, manifest bool) error {
		switch {
		case d.Digest == l.Descriptor.Digest || referrers[d.Digest]:
		case manifest:
			stack = append(stack, d)
		case isNonDistributable(d.MediaType):
		default:
			blobs = append(blobs, d)
		}
		return nil
	})
	for i := len(stack) - 1; i >= 0; i-- {
		manifests = append(manifests, stack[i])
	}
	return blobs, manifests, err
}

func pushLayout(ctx context.Context, c *oci.Client, l Layout, repo oci.Reference, tags []string) error {
	blobs, manifests, err := layoutContents(ctx, l)
	if err != nil {
		return err
	}
	sources, err := mountSources(ctx, l, repo)
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
		g.Go(func() error { return pushBlob(gctx, c, repo, sources[b.Digest], l.Path, b) })
	}
	if err := g.Wait(); err != nil {
		return err
	}
	for _, m := range manifests {
		if err := pushManifest(ctx, c, repo, l, m); err != nil {
			return err
		}
	}
	if err := pushManifest(ctx, c, repo, l, l.Descriptor, tags...); err != nil {
		return err
	}
	for _, m := range l.Referrers {
		if err := pushManifest(ctx, c, repo, l, m); err != nil {
			return err
		}
	}
	return nil
}

// pushBlob uploads d, or mounts it from from's repository when from names one.
func pushBlob(ctx context.Context, c *oci.Client, repo, from oci.Reference, layout string, d ocispec.Descriptor) error {
	if err := checkDigest(d.Digest); err != nil {
		return fmt.Errorf("blob %q: %w", d.Digest, err)
	}
	f, err := os.Open(blobPath(layout, d.Digest))
	if err != nil {
		return err
	}
	defer f.Close()
	if from.Repository != "" {
		err = c.MountBlob(ctx, repo, from, d, f)
	} else {
		err = c.PushBlob(ctx, repo, d, f)
	}
	if err != nil {
		return fmt.Errorf("blob %s: %w", d.Digest, err)
	}
	return nil
}

func pushManifest(ctx context.Context, c *oci.Client, repo oci.Reference, l Layout, m ocispec.Descriptor, tags ...string) error {
	body, err := l.readBlob(ctx, m)
	if err != nil {
		return err
	}
	raw := oci.RawManifest{Descriptor: ocispec.Descriptor{MediaType: m.MediaType, Digest: m.Digest, Size: m.Size}, Body: body}
	if _, err := c.PushManifest(ctx, repo, raw, tags...); err != nil {
		return fmt.Errorf("manifest %s: %w", m.Digest, err)
	}
	return nil
}
