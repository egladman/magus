//go:build !wasm

package std

import (
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

//go:generate go run ../cmd/magus-utils bindings -module image -lang buzz -out ../internal/interp/bindings/gen/image.go -pkg bindgen

// Image is the Buzz image module: it assembles OCI Image Layouts from files a target
// already wrote, and pulls and pushes them. Nothing in it runs a program.
var Image = Module{
	Name: "image",
	Doc:  "Assemble OCI images from files, and pull and push them. Each layout is an OCI Image Layout holding one Image Manifest or Image Index; a stage, layer, platform, index and push is each its own target.",
	Methods: []Method{
		{
			Name:    "assemble",
			Doc:     "Write the Image Manifest spec describes as a layout at layout: the base's layers, at most one new layer, the config and the manifest. Never touches the network.",
			Args:    []Arg{{Name: "spec", Type: TypeAnyMap, Object: "Spec"}, {Name: "layout", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Layout"}},
			Raises:  true,
			Impl:    ImageAssemble,
		},
		{
			Name:    "layout",
			Doc:     "List every entry the new layer of spec holds, in added.entries: path, kind, mode, owner, size, mtime and the Spec item that placed it. It is the list assemble writes, read from the spec's host files without writing; there are no digests yet, and the base is never opened. A dry run of assemble returns the same.",
			Args:    []Arg{{Name: "spec", Type: TypeAnyMap, Object: "Spec"}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Layout"}},
			Raises:  true,
			Impl:    ImageLayout,
		},
		{
			Name:    "assemble_index",
			Doc:     "Write one Image Index over single-platform layouts as a layout at layout. Never touches the network.",
			Args:    []Arg{{Name: "layouts", Type: TypeStringSlice}, {Name: "layout", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Layout"}},
			Raises:  true,
			Impl:    ImageAssembleIndex,
		},
		{
			Name: "pull",
			Doc:  "Fetch reference for platform into a layout at layout. A name@sha256:<hex> reference is content-addressed and caches like any input; a tag-only reference is refused outside the update charm.",
			Args: []Arg{
				{Name: "reference", Type: TypeString},
				{Name: "platform", Type: TypeAnyMap, Object: "Platform"},
				{Name: "layout", Type: TypeString},
				{Name: "auth", Type: TypeAnyMap, Object: "RegistryAuth", Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Layout"}},
			Raises:  true,
			Impl:    ImagePull,
		},
		{
			Name: "push",
			Doc:  "Push the layout's image, its blobs and its referrers to repository, then tag it with each of tags; empty tags pushes by digest only. On a registry without the Referrers API each referrer is also listed in the subject's sha256-<hex> tag index.",
			Args: []Arg{
				{Name: "layout", Type: TypeString},
				{Name: "repository", Type: TypeString},
				{Name: "tags", Type: TypeStringSlice},
				{Name: "auth", Type: TypeAnyMap, Object: "RegistryAuth", Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Pushed"}},
			Raises:  true,
			Impl:    ImagePush,
		},
		{
			Name:    "writeSBOM",
			Doc:     "Write an SPDX 2.3 document naming the layout's image by digest to path.",
			Args:    []Arg{{Name: "layout", Type: TypeString}, {Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Document"}},
			Raises:  true,
			Impl:    ImageWriteSBOM,
		},
		{
			Name:    "write_provenance",
			Doc:     "Write an in-toto Statement v1 with SLSA v1 provenance for the layout's image to path. The digest of each base it was assembled over joins the dependencies.",
			Args:    []Arg{{Name: "layout", Type: TypeString}, {Name: "provenance", Type: TypeAnyMap, Object: "Provenance"}, {Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Document"}},
			Raises:  true,
			Impl:    ImageWriteProvenance,
		},
		{
			Name:    "attach_referrers",
			Doc:     "Write a new layout at layout holding subject's image and one OCI 1.1 referrer per document. subject is never edited.",
			Args:    []Arg{{Name: "subject", Type: TypeString}, {Name: "documents", Type: TypeAny, Object: "[Document]"}, {Name: "layout", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Layout"}},
			Raises:  true,
			Impl:    ImageAttachReferrers,
		},
		{
			Name:    "open",
			Doc:     "Read the layout at layout back: the image whose org.opencontainers.image.ref.name is name (the only image when name is empty), the referrers whose subject it is, and for a single-platform image whose index.json entry names its platform, that platform, config and layers.",
			Args:    []Arg{{Name: "layout", Type: TypeString}, {Name: "name", Type: TypeString, Optional: true}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Layout"}},
			Raises:  true,
			Impl:    ImageOpen,
		},
		{
			Name:    "referrers",
			Doc:     "Read the documents attached beside the image at layout (the SBOM and provenance referrers) without a registry: one Document per referrer layer, its Path the blob in the layout so fs\\read reads it, its Descriptor the layer's with the referrer's artifactType. name picks the image as open does.",
			Args:    []Arg{{Name: "layout", Type: TypeString}, {Name: "name", Type: TypeString, Optional: true}},
			Returns: []Ret{{Type: TypeAny, Object: "[Document]"}},
			Raises:  true,
			Impl:    ImageReferrers,
		},
		{
			Name:    "parsePlatform",
			Doc:     "Parse \"linux/amd64\", \"linux/amd64/v3\" or \"linux/arm64/v8\" into a Platform.",
			Args:    []Arg{{Name: "spec", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Platform"}},
			Raises:  true,
			Impl:    ImageParsePlatform,
		},
		{
			Name:    "parsePlatforms",
			Doc:     "Parse each of specs into a Platform, refusing two that name the same platform: the set a multi-arch build fans out over.",
			Args:    []Arg{{Name: "specs", Type: TypeStringSlice}},
			Returns: []Ret{{Type: TypeAny, Object: "[Platform]"}},
			Raises:  true,
			Impl:    ImageParsePlatforms,
		},
		{
			Name:    "go_env",
			Doc:     "The GOOS, GOARCH and, for an amd64 variant, GOAMD64 that make go build target platform, for a build target's ctx.withEnv.",
			Args:    []Arg{{Name: "platform", Type: TypeAnyMap, Object: "Platform"}},
			Returns: []Ret{{Type: TypeStringMap}},
			Raises:  true,
			Impl:    ImageGoEnv,
		},
	},
}

// ImageAssemble writes the image spec describes as a layout at dest. A dry run
// writes nothing and returns what ImageLayout returns.
func ImageAssemble(ctx context.Context, spec types.ImageSpec, dest string) (types.ImageLayout, error) {
	if types.Tracing(ctx) {
		return ImageLayout(ctx, spec)
	}
	s, err := imageSpec(ctx, spec)
	if err != nil {
		return types.ImageLayout{}, fmt.Errorf("image.assemble: %w", err)
	}
	dest = resolvePath(ctx, dest)
	if err := imageCheck(ctx, imageReads(s), []string{dest}); err != nil {
		return types.ImageLayout{}, fmt.Errorf("image.assemble: %w", err)
	}
	l, err := image.Assemble(ctx, s, dest)
	if err != nil {
		return types.ImageLayout{}, err
	}
	return manifestToTypes(l), nil
}

// ImageLayout lists every entry the new layer of spec holds, from the list
// assemble writes. It reads the spec's host files, never the base, and writes
// nothing, so it runs the same under a dry run.
func ImageLayout(ctx context.Context, spec types.ImageSpec) (types.ImageLayout, error) {
	s, err := imageSpec(ctx, spec)
	if err != nil {
		return types.ImageLayout{}, fmt.Errorf("image.layout: %w", err)
	}
	if err := imageCheck(ctx, imageReads(s), nil); err != nil {
		return types.ImageLayout{}, fmt.Errorf("image.layout: %w", err)
	}
	entries, err := image.Entries(ctx, s)
	if err != nil {
		return types.ImageLayout{}, err
	}
	p := platformToTypes(s.Platform)
	return types.ImageLayout{Platform: &p, Added: listedToTypes(entries)}, nil
}

// ImageAssembleIndex writes one Image Index over layouts at dest.
func ImageAssembleIndex(ctx context.Context, layouts []string, dest string) (types.ImageLayout, error) {
	if types.Tracing(ctx) {
		return types.ImageLayout{}, nil
	}
	resolved := make([]string, len(layouts))
	for i, l := range layouts {
		resolved[i] = resolvePath(ctx, l)
	}
	dest = resolvePath(ctx, dest)
	if err := imageCheck(ctx, resolved, []string{dest}); err != nil {
		return types.ImageLayout{}, fmt.Errorf("image.assembleIndex: %w", err)
	}
	l, err := image.AssembleIndex(ctx, resolved, dest)
	if err != nil {
		return types.ImageLayout{}, err
	}
	return layoutToTypes(l), nil
}

// ImagePull fetches reference into a layout at dest. A tag-only reference is accepted
// only under the update charm: it names what a registry serves today, which the cache
// key cannot see.
func ImagePull(ctx context.Context, reference string, platform types.ImagePlatform, dest string, auth *types.ImageRegistryAuth) (types.ImageLayout, error) {
	if types.Tracing(ctx) {
		return types.ImageLayout{}, nil
	}
	ref, err := oci.ParseReference(reference)
	if err != nil {
		return types.ImageLayout{}, fmt.Errorf("image.pull: %w", err)
	}
	dest = resolvePath(ctx, dest)
	if err := imageCheck(ctx, nil, []string{dest}); err != nil {
		return types.ImageLayout{}, fmt.Errorf("image.pull: %w", err)
	}
	client := imageClient(auth)
	if ref.Digest == "" {
		if !types.HasCharm(ctx, types.CharmUpdate) {
			return types.ImageLayout{}, fmt.Errorf("image.pull: %s names a tag, which can move: pin it as name@sha256:<hex>, or run the target under the update charm (magus run <target>:update) to resolve it", reference)
		}
		resolved, err := client.Resolve(ctx, ref)
		if err != nil {
			return types.ImageLayout{}, fmt.Errorf("image.pull: %w", err)
		}
		ref.Digest = resolved.Digest
	}
	l, err := image.Pull(ctx, client, ref, platformFromTypes(platform), dest)
	if err != nil {
		return types.ImageLayout{}, err
	}
	return manifestToTypes(l), nil
}

// ImagePush pushes the layout at layout to repository under tags.
func ImagePush(ctx context.Context, layout, repository string, tags []string, auth *types.ImageRegistryAuth) (types.ImagePushed, error) {
	if types.Tracing(ctx) {
		return types.ImagePushed{}, nil
	}
	repo, err := oci.ParseRepository(repository)
	if err != nil {
		return types.ImagePushed{}, fmt.Errorf("image.push: %w", err)
	}
	layout = resolvePath(ctx, layout)
	if err := imageCheck(ctx, []string{layout}, nil); err != nil {
		return types.ImagePushed{}, fmt.Errorf("image.push: %w", err)
	}
	pinned, err := image.Push(ctx, imageClient(auth), layout, repo, tags...)
	if err != nil {
		return types.ImagePushed{}, err
	}
	return types.ImagePushed{
		Repository: repo.String(),
		Digest:     digestToTypes(pinned.Digest),
		Tags:       slices.Clone(tags),
		Reference:  pinned.String(),
	}, nil
}

// ImageWriteSBOM writes an SPDX document for the layout's image to path.
func ImageWriteSBOM(ctx context.Context, layout, path string) (types.ImageDocument, error) {
	if types.Tracing(ctx) {
		return types.ImageDocument{}, nil
	}
	layout, path = resolvePath(ctx, layout), resolvePath(ctx, path)
	if err := imageCheck(ctx, []string{layout}, []string{path}); err != nil {
		return types.ImageDocument{}, fmt.Errorf("image.sbom: %w", err)
	}
	doc, err := image.WriteSBOM(ctx, layout, path)
	if err != nil {
		return types.ImageDocument{}, err
	}
	return documentToTypes(doc), nil
}

// ImageWriteProvenance writes an in-toto statement for the layout's image to path.
func ImageWriteProvenance(ctx context.Context, layout string, p types.ImageProvenance, path string) (types.ImageDocument, error) {
	if types.Tracing(ctx) {
		return types.ImageDocument{}, nil
	}
	prov := image.Provenance{BuilderID: p.BuilderID, SourceRevision: p.SourceRevision}
	for i, m := range p.Dependencies {
		d, err := digestFromTypes(m.Digest)
		if err != nil {
			return types.ImageDocument{}, fmt.Errorf("image.provenance: dependencies[%d]: %w", i, err)
		}
		prov.Dependencies = append(prov.Dependencies, image.Dependency{URI: m.URI, Digest: d})
	}
	layout, path = resolvePath(ctx, layout), resolvePath(ctx, path)
	if err := imageCheck(ctx, []string{layout}, []string{path}); err != nil {
		return types.ImageDocument{}, fmt.Errorf("image.provenance: %w", err)
	}
	doc, err := image.WriteProvenance(ctx, layout, prov, path)
	if err != nil {
		return types.ImageDocument{}, err
	}
	return documentToTypes(doc), nil
}

// ImageAttachReferrers writes a new layout at dest with subject's image and documents.
func ImageAttachReferrers(ctx context.Context, subject string, documents []types.ImageDocument, dest string) (types.ImageLayout, error) {
	if types.Tracing(ctx) {
		return types.ImageLayout{}, nil
	}
	subject, dest = resolvePath(ctx, subject), resolvePath(ctx, dest)
	docs := make([]image.Document, len(documents))
	reads := []string{subject}
	for i, d := range documents {
		desc, err := descriptorFromTypes(d.Descriptor)
		if err != nil {
			return types.ImageLayout{}, fmt.Errorf("image.attachReferrers: documents[%d]: %w", i, err)
		}
		docs[i] = image.Document{Path: resolvePath(ctx, d.Path), Descriptor: desc}
		reads = append(reads, docs[i].Path)
	}
	if err := imageCheck(ctx, reads, []string{dest}); err != nil {
		return types.ImageLayout{}, fmt.Errorf("image.attachReferrers: %w", err)
	}
	l, err := image.AttachReferrers(ctx, subject, docs, dest)
	if err != nil {
		return types.ImageLayout{}, err
	}
	return layoutToTypes(l), nil
}

// ImageOpen reads the layout at path back, opened at the image name names.
func ImageOpen(ctx context.Context, path, name string) (types.ImageLayout, error) {
	if types.Tracing(ctx) {
		return types.ImageLayout{}, nil
	}
	path = resolvePath(ctx, path)
	if err := imageCheck(ctx, []string{path}, nil); err != nil {
		return types.ImageLayout{}, fmt.Errorf("image.open: %w", err)
	}
	l, err := image.OpenLayout(ctx, path, name)
	if err != nil {
		return types.ImageLayout{}, err
	}
	// A manifest's platform is in its config; the index.json entry names it for every
	// layout magus writes, and without it there is no platform to ask Manifest for.
	if l.Descriptor.MediaType != ocispec.MediaTypeImageManifest || l.Descriptor.Platform == nil {
		return layoutToTypes(l), nil
	}
	p := l.Descriptor.Platform
	m, err := l.Manifest(ctx, image.Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant})
	if err != nil {
		return types.ImageLayout{}, fmt.Errorf("image.open: %w", err)
	}
	return manifestToTypes(m), nil
}

// ImageReferrers reads the documents attached beside the image at path.
// It needs no registry, which oci.Client.Referrers does.
func ImageReferrers(ctx context.Context, path, name string) ([]types.ImageDocument, error) {
	if types.Tracing(ctx) {
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
	docs := []types.ImageDocument{}
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
			docs = append(docs, types.ImageDocument{Path: layoutBlobPath(l.Path, layer.Digest), Descriptor: descriptorToTypes(layer)})
		}
	}
	return docs, nil
}

func layoutBlobPath(layout string, d digest.Digest) string {
	return filepath.Join(layout, "blobs", d.Algorithm().String(), d.Encoded())
}

// ImageParsePlatform parses "os/architecture[/variant]".
func ImageParsePlatform(_ context.Context, spec string) (types.ImagePlatform, error) {
	p, err := image.ParsePlatform(spec)
	if err != nil {
		return types.ImagePlatform{}, err
	}
	return platformToTypes(p), nil
}

// ImageParsePlatforms parses each spec, refusing duplicates.
func ImageParsePlatforms(_ context.Context, specs []string) ([]types.ImagePlatform, error) {
	ps, err := image.ParsePlatforms(specs...)
	if err != nil {
		return nil, err
	}
	out := make([]types.ImagePlatform, len(ps))
	for i, p := range ps {
		out[i] = platformToTypes(p)
	}
	return out, nil
}

// ImageGoEnv is the GOOS, GOARCH and GOAMD64 environment for platform.
func ImageGoEnv(_ context.Context, platform types.ImagePlatform) (map[string]string, error) {
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
func imageClient(auth *types.ImageRegistryAuth) *oci.Client {
	if auth == nil {
		return &oci.Client{}
	}
	return &oci.Client{Credentials: oci.Credentials{Username: auth.Username, Password: types.NewSecret(auth.Password.Value)}}
}

func imageSpec(ctx context.Context, in types.ImageSpec) (image.Spec, error) {
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

func platformFromTypes(p types.ImagePlatform) image.Platform {
	return image.Platform{OS: string(p.OS), Architecture: string(p.Architecture), Variant: string(p.Variant), OSVersion: p.OSVersion, OSFeatures: p.OSFeatures}
}

func platformToTypes(p image.Platform) types.ImagePlatform {
	return types.ImagePlatform{
		OS: types.ImageOS(p.OS), Architecture: types.ImageArchitecture(p.Architecture), Variant: types.ImageVariant(p.Variant),
		OSVersion: p.OSVersion, OSFeatures: p.OSFeatures,
	}
}

// ociPlatformToTypes converts a descriptor's platform, which may name one magus does not
// build for, so it does not pass through image.Platform's validation.
func ociPlatformToTypes(p ocispec.Platform) types.ImagePlatform {
	return types.ImagePlatform{
		OS: types.ImageOS(p.OS), Architecture: types.ImageArchitecture(p.Architecture), Variant: types.ImageVariant(p.Variant),
		OSVersion: p.OSVersion, OSFeatures: p.OSFeatures,
	}
}

func digestToTypes(d digest.Digest) types.ImageDigest {
	if d == "" {
		return types.ImageDigest{}
	}
	return types.ImageDigest{Algorithm: d.Algorithm().String(), Encoded: d.Encoded()}
}

func digestFromTypes(d types.ImageDigest) (digest.Digest, error) {
	return oci.ParseDigest(d.String())
}

func descriptorToTypes(d ocispec.Descriptor) types.ImageDescriptor {
	out := types.ImageDescriptor{
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

func descriptorFromTypes(d types.ImageDescriptor) (ocispec.Descriptor, error) {
	dg, err := digestFromTypes(d.Digest)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return ocispec.Descriptor{MediaType: d.MediaType, ArtifactType: d.ArtifactType, Digest: dg, Size: int64(d.Size), Annotations: d.Annotations}, nil
}

func descriptorsToTypes(ds []ocispec.Descriptor) []types.ImageDescriptor {
	out := make([]types.ImageDescriptor, len(ds))
	for i, d := range ds {
		out[i] = descriptorToTypes(d)
	}
	return out
}

func layoutToTypes(l image.Layout) types.ImageLayout {
	return types.ImageLayout{Path: l.Path, Descriptor: descriptorToTypes(l.Descriptor), Referrers: descriptorsToTypes(l.Referrers)}
}

func manifestToTypes(m image.Manifest) types.ImageLayout {
	out := layoutToTypes(m.Layout)
	p := platformToTypes(m.Platform)
	cfg := descriptorToTypes(m.Config)
	out.Platform, out.Config, out.Layers, out.Created = &p, &cfg, descriptorsToTypes(m.Layers), m.Created
	if m.Added != nil {
		out.Added = &types.ImageAddedLayer{
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
func listedToTypes(entries []image.Entry) *types.ImageAddedLayer {
	if len(entries) == 0 {
		return nil
	}
	out := &types.ImageAddedLayer{Entries: entriesToTypes(entries)}
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

func entriesToTypes(entries []image.Entry) []types.ImageEntry {
	out := make([]types.ImageEntry, len(entries))
	for i, e := range entries {
		out[i] = types.ImageEntry{
			Path: e.Path, Kind: types.ImageEntryKind(e.Kind), Mode: e.Mode.String(), UID: e.UID, GID: e.GID,
			Size: int(e.Size), Target: e.Target, ModTime: e.ModTime.UTC().Format(time.RFC3339), Origin: e.Origin,
		}
	}
	return out
}

func documentToTypes(d image.Document) types.ImageDocument {
	return types.ImageDocument{Path: d.Path, Descriptor: descriptorToTypes(d.Descriptor)}
}
