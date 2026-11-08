//go:build !wasm

package std

import (
	imagetypes "github.com/egladman/magus/types/image"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/libs/image"
	"github.com/egladman/magus/libs/oci"
	"github.com/egladman/magus/types"
)

// Image is the Buzz image module: it assembles OCI Image Layouts from files a target
// already wrote, and pulls and pushes them. Nothing in it runs a program.
var Image = Module{
	Name: "image",
	Doc:  "Assemble OCI images from files, and pull and push them. Each layout is an OCI Image Layout holding one Image Manifest or Image Index; a stage, layer, platform, index and push is each its own target.",
	Methods: []Method{
		{
			Name:    "assemble",
			Doc:     "Write the Image Manifest spec describes as a layout at layout: the base's layers, at most one new layer, the config and the manifest. Never touches the network.",
			Args:    []Arg{{Name: "spec", Object: "Spec"}, {Name: "layout"}},
			Returns: []Ret{{Object: "Layout"}},
			Raises:  true,
			Impl:    ImageAssemble,
		},
		{
			Name:    "layout",
			Doc:     "List every entry the new layer of spec holds, in added.entries: path, kind, mode, owner, size, mtime and the Spec item that placed it. It is the list assemble writes, read from the spec's host files without writing; there are no digests yet, and the base is never opened. A dry run of assemble returns the same.",
			Args:    []Arg{{Name: "spec", Object: "Spec"}},
			Returns: []Ret{{Object: "Layout"}},
			Raises:  true,
			Impl:    ImageLayout,
		},
		{
			Name:    "assemble_index",
			Doc:     "Write one Image Index over single-platform layouts as a layout at layout. Never touches the network.",
			Args:    []Arg{{Name: "layouts"}, {Name: "layout"}},
			Returns: []Ret{{Object: "Layout"}},
			Raises:  true,
			Impl:    ImageAssembleIndex,
		},
		{
			Name: "pull",
			Doc:  "Fetch reference for platform into a layout at layout. A name@sha256:<hex> reference is content-addressed and caches like any input; a tag-only reference is refused outside the update charm.",
			Args: []Arg{
				{Name: "reference"},
				{Name: "platform", Object: "Platform"},
				{Name: "layout"},
				{Name: "auth", Object: "RegistryAuth", Optional: true},
			},
			Returns: []Ret{{Object: "Layout"}},
			Raises:  true,
			Impl:    ImagePull,
		},
		{
			Name: "push",
			Doc:  "Push the layout's image, its blobs and its referrers to repository, then tag it with each of tags; empty tags pushes by digest only. On a registry without the Referrers API each referrer is also listed in the subject's sha256-<hex> tag index.",
			Args: []Arg{
				{Name: "layout"},
				{Name: "repository"},
				{Name: "tags"},
				{Name: "auth", Object: "RegistryAuth", Optional: true},
			},
			Returns: []Ret{{Object: "Pushed"}},
			Raises:  true,
			Impl:    ImagePush,
		},
		{
			Name:    "writeSBOM",
			Doc:     "Write an SPDX 2.3 document naming the layout's image by digest to path.",
			Args:    []Arg{{Name: "layout"}, {Name: "path"}},
			Returns: []Ret{{Object: "Document"}},
			Raises:  true,
			Impl:    ImageWriteSBOM,
		},
		{
			Name:    "write_provenance",
			Doc:     "Write an in-toto Statement v1 with SLSA v1 provenance for the layout's image to path. The digest of each base it was assembled over joins the dependencies.",
			Args:    []Arg{{Name: "layout"}, {Name: "provenance", Object: "Provenance"}, {Name: "path"}},
			Returns: []Ret{{Object: "Document"}},
			Raises:  true,
			Impl:    ImageWriteProvenance,
		},
		{
			Name:    "attach_referrers",
			Doc:     "Write a new layout at layout holding subject's image and one OCI 1.1 referrer per document. subject is never edited.",
			Args:    []Arg{{Name: "subject"}, {Name: "documents", Object: "[Document]"}, {Name: "layout"}},
			Returns: []Ret{{Object: "Layout"}},
			Raises:  true,
			Impl:    ImageAttachReferrers,
		},
		{
			Name:    "open",
			Doc:     "Read the layout at layout back: the image whose org.opencontainers.image.ref.name is name (the only image when name is empty), the referrers whose subject it is, and for a single-platform image whose index.json entry names its platform, that platform, config and layers.",
			Args:    []Arg{{Name: "layout"}, {Name: "name", Optional: true}},
			Returns: []Ret{{Object: "Layout"}},
			Raises:  true,
			Impl:    ImageOpen,
		},
		{
			Name:    "referrers",
			Doc:     "Read the documents attached beside the image at layout (the SBOM and provenance referrers) without a registry: one Document per referrer layer, its Path the blob in the layout so fs\\read reads it, its Descriptor the layer's with the referrer's artifactType. name picks the image as open does.",
			Args:    []Arg{{Name: "layout"}, {Name: "name", Optional: true}},
			Returns: []Ret{{Object: "[Document]"}},
			Raises:  true,
			Impl:    ImageReferrers,
		},
		{
			Name:    "parsePlatform",
			Doc:     "Parse \"linux/amd64\", \"linux/amd64/v3\" or \"linux/arm64/v8\" into a Platform.",
			Args:    []Arg{{Name: "spec"}},
			Returns: []Ret{{Object: "Platform"}},
			Raises:  true,
			Impl:    ImageParsePlatform,
		},
		{
			Name:    "parsePlatforms",
			Doc:     "Parse each of specs into a Platform, refusing two that name the same platform: the set a multi-arch build fans out over.",
			Args:    []Arg{{Name: "specs"}},
			Returns: []Ret{{Object: "[Platform]"}},
			Raises:  true,
			Impl:    ImageParsePlatforms,
		},
		{
			Name:    "go_env",
			Doc:     "The GOOS, GOARCH and, for an amd64 variant, GOAMD64 that make go build target platform, for a build target's ctx.withEnv.",
			Args:    []Arg{{Name: "platform", Object: "Platform"}},
			Returns: []Ret{{}},
			Raises:  true,
			Impl:    ImageGoEnv,
		},
	},
	Objects: []Object{{
		Name: "Digest",
		Methods: []Method{{
			Name:    "string",
			Doc:     "The digest as OCI writes it, \"sha256:<hex>\", and \"\" when unset.",
			Returns: []Ret{{}},
			Impl:    ImageDigestString,
		}},
	}},
}

// ImageDigestString is Digest.string() in Buzz.
func ImageDigestString(_ context.Context, d imagetypes.Digest) (string, error) {
	return d.String(), nil
}

// ImageAssemble writes the image spec describes as a layout at dest. A dry run
// writes nothing and returns what ImageLayout returns.
func ImageAssemble(ctx context.Context, spec imagetypes.Spec, dest string) (imagetypes.Layout, error) {
	if types.Stubbed(ctx, "image.assemble") {
		return ImageLayout(ctx, spec)
	}
	s, err := imageSpec(ctx, spec)
	if err != nil {
		return imagetypes.Layout{}, fmt.Errorf("image.assemble: %w", err)
	}
	dest = resolvePath(ctx, dest)
	if err := imageCheck(ctx, imageReads(s), []string{dest}); err != nil {
		return imagetypes.Layout{}, fmt.Errorf("image.assemble: %w", err)
	}
	l, err := image.Assemble(ctx, s, dest)
	if err != nil {
		return imagetypes.Layout{}, err
	}
	return manifestToTypes(l), nil
}

// ImageLayout lists every entry the new layer of spec holds, from the list
// assemble writes. It reads the spec's host files, never the base, and writes
// nothing, so it runs the same under a dry run.
func ImageLayout(ctx context.Context, spec imagetypes.Spec) (imagetypes.Layout, error) {
	s, err := imageSpec(ctx, spec)
	if err != nil {
		return imagetypes.Layout{}, fmt.Errorf("image.layout: %w", err)
	}
	if err := imageCheck(ctx, imageReads(s), nil); err != nil {
		return imagetypes.Layout{}, fmt.Errorf("image.layout: %w", err)
	}
	entries, err := image.Entries(ctx, s)
	if err != nil {
		return imagetypes.Layout{}, err
	}
	p := platformToTypes(s.Platform)
	return imagetypes.Layout{Platform: &p, Added: listedToTypes(entries)}, nil
}

// ImageAssembleIndex writes one Image Index over layouts at dest.
func ImageAssembleIndex(ctx context.Context, layouts []string, dest string) (imagetypes.Layout, error) {
	if types.Stubbed(ctx, "image.assembleIndex") {
		return imagetypes.Layout{}, nil
	}
	resolved := make([]string, len(layouts))
	for i, l := range layouts {
		resolved[i] = resolvePath(ctx, l)
	}
	dest = resolvePath(ctx, dest)
	if err := imageCheck(ctx, resolved, []string{dest}); err != nil {
		return imagetypes.Layout{}, fmt.Errorf("image.assembleIndex: %w", err)
	}
	l, err := image.AssembleIndex(ctx, resolved, dest)
	if err != nil {
		return imagetypes.Layout{}, err
	}
	return layoutToTypes(l), nil
}

// ImagePull fetches reference into a layout at dest. A tag-only reference is accepted
// only under the update charm: it names what a registry serves today, which the cache
// key cannot see.
func ImagePull(ctx context.Context, reference string, platform imagetypes.Platform, dest string, auth *imagetypes.RegistryAuth) (imagetypes.Layout, error) {
	if types.Stubbed(ctx, "image.pull") {
		return imagetypes.Layout{}, nil
	}
	ref, err := oci.ParseReference(reference)
	if err != nil {
		return imagetypes.Layout{}, fmt.Errorf("image.pull: %w", err)
	}
	dest = resolvePath(ctx, dest)
	if err := imageCheck(ctx, nil, []string{dest}); err != nil {
		return imagetypes.Layout{}, fmt.Errorf("image.pull: %w", err)
	}
	client := imageClient(auth)
	if ref.Digest == "" {
		if !types.HasCharm(ctx, types.CharmUpdate) {
			return imagetypes.Layout{}, fmt.Errorf("image.pull: %s names a tag, which can move: pin it as name@sha256:<hex>, or run the target under the update charm (magus run <target>:update) to resolve it", reference)
		}
		resolved, err := client.Resolve(ctx, ref)
		if err != nil {
			return imagetypes.Layout{}, fmt.Errorf("image.pull: %w", err)
		}
		ref.Digest = resolved.Digest
	}
	l, err := image.Pull(ctx, client, ref, platformFromTypes(platform), dest)
	if err != nil {
		return imagetypes.Layout{}, err
	}
	return manifestToTypes(l), nil
}

// ImagePush pushes the layout at layout to repository under tags.
func ImagePush(ctx context.Context, layout, repository string, tags []string, auth *imagetypes.RegistryAuth) (imagetypes.Pushed, error) {
	if types.Stubbed(ctx, "image.push") {
		return imagetypes.Pushed{}, nil
	}
	repo, err := oci.ParseRepository(repository)
	if err != nil {
		return imagetypes.Pushed{}, fmt.Errorf("image.push: %w", err)
	}
	layout = resolvePath(ctx, layout)
	if err := imageCheck(ctx, []string{layout}, nil); err != nil {
		return imagetypes.Pushed{}, fmt.Errorf("image.push: %w", err)
	}
	pinned, err := image.Push(ctx, imageClient(auth), layout, repo, tags...)
	if err != nil {
		return imagetypes.Pushed{}, err
	}
	return imagetypes.Pushed{
		Repository: repo.String(),
		Digest:     digestToTypes(pinned.Digest),
		Tags:       slices.Clone(tags),
		Reference:  pinned.String(),
	}, nil
}

// ImageWriteSBOM writes an SPDX document for the layout's image to path.
func ImageWriteSBOM(ctx context.Context, layout, path string) (imagetypes.Document, error) {
	if types.Stubbed(ctx, "image.writeSBOM") {
		return imagetypes.Document{}, nil
	}
	layout, path = resolvePath(ctx, layout), resolvePath(ctx, path)
	if err := imageCheck(ctx, []string{layout}, []string{path}); err != nil {
		return imagetypes.Document{}, fmt.Errorf("image.sbom: %w", err)
	}
	doc, err := image.WriteSBOM(ctx, layout, path)
	if err != nil {
		return imagetypes.Document{}, err
	}
	return documentToTypes(doc), nil
}

// ImageWriteProvenance writes an in-toto statement for the layout's image to path.
func ImageWriteProvenance(ctx context.Context, layout string, p imagetypes.Provenance, path string) (imagetypes.Document, error) {
	if types.Stubbed(ctx, "image.writeProvenance") {
		return imagetypes.Document{}, nil
	}
	prov := image.Provenance{BuilderID: p.BuilderID, SourceRevision: p.SourceRevision}
	for i, m := range p.Dependencies {
		d, err := digestFromTypes(m.Digest)
		if err != nil {
			return imagetypes.Document{}, fmt.Errorf("image.provenance: dependencies[%d]: %w", i, err)
		}
		prov.Dependencies = append(prov.Dependencies, image.Dependency{URI: m.URI, Digest: d})
	}
	layout, path = resolvePath(ctx, layout), resolvePath(ctx, path)
	if err := imageCheck(ctx, []string{layout}, []string{path}); err != nil {
		return imagetypes.Document{}, fmt.Errorf("image.provenance: %w", err)
	}
	doc, err := image.WriteProvenance(ctx, layout, prov, path)
	if err != nil {
		return imagetypes.Document{}, err
	}
	return documentToTypes(doc), nil
}

// ImageAttachReferrers writes a new layout at dest with subject's image and documents.
func ImageAttachReferrers(ctx context.Context, subject string, documents []imagetypes.Document, dest string) (imagetypes.Layout, error) {
	if types.Stubbed(ctx, "image.attachReferrers") {
		return imagetypes.Layout{}, nil
	}
	subject, dest = resolvePath(ctx, subject), resolvePath(ctx, dest)
	docs := make([]image.Document, len(documents))
	reads := []string{subject}
	for i, d := range documents {
		desc, err := descriptorFromTypes(d.Descriptor)
		if err != nil {
			return imagetypes.Layout{}, fmt.Errorf("image.attachReferrers: documents[%d]: %w", i, err)
		}
		docs[i] = image.Document{Path: resolvePath(ctx, d.Path), Descriptor: desc}
		reads = append(reads, docs[i].Path)
	}
	if err := imageCheck(ctx, reads, []string{dest}); err != nil {
		return imagetypes.Layout{}, fmt.Errorf("image.attachReferrers: %w", err)
	}
	l, err := image.AttachReferrers(ctx, subject, docs, dest)
	if err != nil {
		return imagetypes.Layout{}, err
	}
	return layoutToTypes(l), nil
}

// ImageOpen reads the layout at path back, opened at the image name names.
func ImageOpen(ctx context.Context, path, name string) (imagetypes.Layout, error) {
	if types.Stubbed(ctx, "image.open") {
		return imagetypes.Layout{}, nil
	}
	path = resolvePath(ctx, path)
	if err := imageCheck(ctx, []string{path}, nil); err != nil {
		return imagetypes.Layout{}, fmt.Errorf("image.open: %w", err)
	}
	l, err := image.OpenLayout(ctx, path, name)
	if err != nil {
		return imagetypes.Layout{}, err
	}
	// A manifest's platform is in its config; the index.json entry names it for every
	// layout magus writes, and without it there is no platform to ask Manifest for.
	if l.Descriptor.MediaType != ocispec.MediaTypeImageManifest || l.Descriptor.Platform == nil {
		return layoutToTypes(l), nil
	}
	p := l.Descriptor.Platform
	m, err := l.Manifest(ctx, image.Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant})
	if err != nil {
		return imagetypes.Layout{}, fmt.Errorf("image.open: %w", err)
	}
	return manifestToTypes(m), nil
}

// ImageReferrers reads the documents attached beside the image at path.
// It needs no registry, which oci.Client.Referrers does.
func ImageReferrers(ctx context.Context, path, name string) ([]imagetypes.Document, error) {
	if types.Stubbed(ctx, "image.referrers") {
		return nil, nil
	}
	path = resolvePath(ctx, path)
	if err := imageCheck(ctx, []string{path}, nil); err != nil {
		return nil, fmt.Errorf("image.referrers: %w", err)
	}
	l, err := image.OpenLayout(ctx, path, name)
	if err != nil {
		return nil, err
	}
	docs := []imagetypes.Document{}
	for _, r := range l.Referrers {
		data, err := os.ReadFile(layoutBlobPath(l.Path, r.Digest))
		if err != nil {
			return nil, fmt.Errorf("image.referrers: %w", err)
		}
		if err := oci.VerifyBlob(ctx, bytes.NewReader(data), r); err != nil {
			return nil, fmt.Errorf("image.referrers: referrer %s: %w", r.Digest, err)
		}
		var m ocispec.Manifest
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("image.referrers: referrer %s: %w", r.Digest, err)
		}
		if len(m.Layers) == 0 {
			return nil, fmt.Errorf("image.referrers: referrer %s (%s) has no layer holding a document", r.Digest, m.ArtifactType)
		}
		for _, layer := range m.Layers {
			layer.ArtifactType = m.ArtifactType
			docs = append(docs, imagetypes.Document{Path: layoutBlobPath(l.Path, layer.Digest), Descriptor: descriptorToTypes(layer)})
		}
	}
	return docs, nil
}

func layoutBlobPath(layout string, d digest.Digest) string {
	return filepath.Join(layout, "blobs", d.Algorithm().String(), d.Encoded())
}

// ImageParsePlatform parses "os/architecture[/variant]".
func ImageParsePlatform(_ context.Context, spec string) (imagetypes.Platform, error) {
	p, err := image.ParsePlatform(spec)
	if err != nil {
		return imagetypes.Platform{}, err
	}
	return platformToTypes(p), nil
}

// ImageParsePlatforms parses each spec, refusing duplicates.
func ImageParsePlatforms(_ context.Context, specs []string) ([]imagetypes.Platform, error) {
	ps, err := image.ParsePlatforms(specs...)
	if err != nil {
		return nil, err
	}
	out := make([]imagetypes.Platform, len(ps))
	for i, p := range ps {
		out[i] = platformToTypes(p)
	}
	return out, nil
}

// ImageGoEnv is the GOOS, GOARCH and GOAMD64 environment for platform.
func ImageGoEnv(_ context.Context, platform imagetypes.Platform) (map[string]string, error) {
	return platformFromTypes(platform).GoEnv()
}

// imageCheck confines reads and writes to the target's sandbox, as archive does.
func imageCheck(ctx context.Context, reads, writes []string) error {
	p := sandbox.PolicyFromContext(ctx)
	if p == nil {
		return nil
	}
	for _, r := range reads {
		if err := p.CheckRead(ctx, r); err != nil {
			return err
		}
	}
	for _, w := range writes {
		if err := p.CheckWrite(ctx, w); err != nil {
			return err
		}
	}
	return nil
}

func imageReads(s image.Spec) []string {
	var out []string
	if s.Base != "" {
		out = append(out, s.Base)
	}
	for _, f := range s.Files {
		out = append(out, f.Source)
	}
	for _, t := range s.Trees {
		out = append(out, t.Source)
	}
	return out
}

// imageClient is the single place a Buzz credential becomes a registry login.
func imageClient(auth *imagetypes.RegistryAuth) *oci.Client {
	if auth == nil {
		return &oci.Client{}
	}
	return &oci.Client{Credentials: oci.Credentials{Username: auth.Username, Password: types.NewSecret(auth.Password.Value)}}
}

func imageSpec(ctx context.Context, in imagetypes.Spec) (image.Spec, error) {
	s := image.Spec{
		Scratch:     in.Scratch,
		Platform:    platformFromTypes(in.Platform),
		Annotations: in.Annotations,
		Created:     in.Created,
		Config: image.Config{
			User: in.Config.User, WorkingDir: in.Config.WorkingDir, StopSignal: in.Config.StopSignal,
			Entrypoint: in.Config.Entrypoint, Cmd: in.Config.Cmd,
			ExposedPorts: in.Config.ExposedPorts, Volumes: in.Config.Volumes,
			Env: in.Config.Env, UnsetEnv: in.Config.UnsetEnv, Labels: in.Config.Labels,
		},
	}
	if in.Base != "" {
		s.Base = resolvePath(ctx, in.Base)
	}
	for i, f := range in.Files {
		mode, err := imageMode(f.Mode)
		if err != nil {
			return image.Spec{}, fmt.Errorf("files[%d] %s: %w", i, f.Path, err)
		}
		s.Files = append(s.Files, image.File{Source: resolvePath(ctx, f.Source), Path: f.Path, Mode: mode, UID: f.UID, GID: f.GID})
	}
	for _, t := range in.Trees {
		s.Trees = append(s.Trees, image.Tree{
			Source: resolvePath(ctx, t.Source), Path: t.Path, Include: t.Include, Exclude: t.Exclude,
			ModePolicy: image.ModePolicy(t.ModePolicy), Symlinks: image.SymlinkPolicy(t.Symlinks), UID: t.UID, GID: t.GID,
		})
	}
	for _, l := range in.Symlinks {
		s.Symlinks = append(s.Symlinks, image.Symlink{Path: l.Path, Target: l.Target, UID: l.UID, GID: l.GID})
	}
	for i, d := range in.Directories {
		mode, err := imageMode(d.Mode)
		if err != nil {
			return image.Spec{}, fmt.Errorf("directories[%d] %s: %w", i, d.Path, err)
		}
		s.Directories = append(s.Directories, image.Directory{Path: d.Path, Mode: mode, UID: d.UID, GID: d.GID})
	}
	return s, nil
}

func imageMode(bits int) (image.Mode, error) {
	if bits < 0 {
		return 0, fmt.Errorf("mode %d is negative", bits)
	}
	m := image.Mode(bits)
	if err := m.Validate(); err != nil {
		return 0, fmt.Errorf("fs\\mode(\"0644\") spells one: %w", err)
	}
	return m, nil
}

func platformFromTypes(p imagetypes.Platform) image.Platform {
	return image.Platform{OS: string(p.OS), Architecture: string(p.Architecture), Variant: string(p.Variant), OSVersion: p.OSVersion, OSFeatures: p.OSFeatures}
}

func platformToTypes(p image.Platform) imagetypes.Platform {
	return imagetypes.Platform{
		OS: imagetypes.OS(p.OS), Architecture: imagetypes.Architecture(p.Architecture), Variant: imagetypes.Variant(p.Variant),
		OSVersion: p.OSVersion, OSFeatures: p.OSFeatures,
	}
}

// ociPlatformToTypes converts a descriptor's platform, which may name one magus does not
// build for, so it does not pass through image.Platform's validation.
func ociPlatformToTypes(p ocispec.Platform) imagetypes.Platform {
	return imagetypes.Platform{
		OS: imagetypes.OS(p.OS), Architecture: imagetypes.Architecture(p.Architecture), Variant: imagetypes.Variant(p.Variant),
		OSVersion: p.OSVersion, OSFeatures: p.OSFeatures,
	}
}

func digestToTypes(d digest.Digest) imagetypes.Digest {
	if d == "" {
		return imagetypes.Digest{}
	}
	return imagetypes.Digest{Algorithm: d.Algorithm().String(), Encoded: d.Encoded()}
}

func digestFromTypes(d imagetypes.Digest) (digest.Digest, error) {
	return oci.ParseDigest(d.String())
}

func descriptorToTypes(d ocispec.Descriptor) imagetypes.Descriptor {
	out := imagetypes.Descriptor{
		MediaType:    d.MediaType,
		ArtifactType: d.ArtifactType,
		Digest:       digestToTypes(d.Digest),
		Size:         int(d.Size),
		Annotations:  d.Annotations,
	}
	if d.Platform != nil {
		p := ociPlatformToTypes(*d.Platform)
		out.Platform = &p
	}
	return out
}

func descriptorFromTypes(d imagetypes.Descriptor) (ocispec.Descriptor, error) {
	dg, err := digestFromTypes(d.Digest)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return ocispec.Descriptor{MediaType: d.MediaType, ArtifactType: d.ArtifactType, Digest: dg, Size: int64(d.Size), Annotations: d.Annotations}, nil
}

func descriptorsToTypes(ds []ocispec.Descriptor) []imagetypes.Descriptor {
	out := make([]imagetypes.Descriptor, len(ds))
	for i, d := range ds {
		out[i] = descriptorToTypes(d)
	}
	return out
}

func layoutToTypes(l image.Layout) imagetypes.Layout {
	return imagetypes.Layout{Path: l.Path, Descriptor: descriptorToTypes(l.Descriptor), Referrers: descriptorsToTypes(l.Referrers)}
}

func manifestToTypes(m image.Manifest) imagetypes.Layout {
	out := layoutToTypes(m.Layout)
	p := platformToTypes(m.Platform)
	cfg := descriptorToTypes(m.Config)
	out.Platform, out.Config, out.Layers, out.Created = &p, &cfg, descriptorsToTypes(m.Layers), m.Created
	if m.Added != nil {
		out.Added = &imagetypes.AddedLayer{
			Descriptor:  descriptorToTypes(m.Added.Descriptor),
			DiffID:      digestToTypes(m.Added.DiffID),
			Entries:     entriesToTypes(m.Added.Entries),
			Files:       m.Added.Files,
			Symlinks:    m.Added.Symlinks,
			Directories: m.Added.Directories,
		}
	}
	return out
}

// listedToTypes is the layer entries would make, before it has a digest; nil
// when there are none, as Assemble adds no layer then.
func listedToTypes(entries []image.Entry) *imagetypes.AddedLayer {
	if len(entries) == 0 {
		return nil
	}
	out := &imagetypes.AddedLayer{Entries: entriesToTypes(entries)}
	for _, e := range entries {
		switch e.Kind {
		case image.EntryFile:
			out.Files++
		case image.EntrySymlink:
			out.Symlinks++
		case image.EntryDirectory:
			out.Directories++
		}
	}
	return out
}

func entriesToTypes(entries []image.Entry) []imagetypes.Entry {
	out := make([]imagetypes.Entry, len(entries))
	for i, e := range entries {
		out[i] = imagetypes.Entry{
			Path: e.Path, Kind: imagetypes.EntryKind(e.Kind), Mode: e.Mode.String(), UID: e.UID, GID: e.GID,
			Size: int(e.Size), Target: e.Target, ModTime: e.ModTime.UTC().Format(time.RFC3339), Origin: e.Origin,
		}
	}
	return out
}

func documentToTypes(d image.Document) imagetypes.Document {
	return imagetypes.Document{Path: d.Path, Descriptor: descriptorToTypes(d.Descriptor)}
}
