//go:build !wasm

package std

import (
	"context"
	"fmt"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/secret"
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
			Doc:  "Push the layout's image, its blobs and its referrers to repository, then tag it with each of tags; empty tags pushes by digest only.",
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
			Name:    "write_sbom",
			Doc:     "Write an SPDX 2.3 document naming the layout's image by digest to path.",
			Args:    []Arg{{Name: "layout", Type: TypeString}, {Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Document"}},
			Raises:  true,
			Impl:    ImageWriteSBOM,
		},
		{
			Name:    "write_provenance",
			Doc:     "Write an in-toto Statement v1 with SLSA v1 provenance for the layout's image to path. The digest of each base it was assembled over joins the materials.",
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
			Doc:     "Read the layout at layout back: its image, its referrers, and for a single-platform image its platform, config and layers.",
			Args:    []Arg{{Name: "layout", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Layout"}},
			Raises:  true,
			Impl:    ImageOpen,
		},
		{
			Name:    "platform",
			Doc:     "Parse \"linux/amd64\" or \"linux/arm64/v8\" into a Platform.",
			Args:    []Arg{{Name: "spec", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Platform"}},
			Raises:  true,
			Impl:    ImageParsePlatform,
		},
		{
			Name:    "platforms",
			Doc:     "Parse each of specs into a Platform, refusing two that name the same platform: the set a multi-arch build fans out over.",
			Args:    []Arg{{Name: "specs", Type: TypeStringSlice}},
			Returns: []Ret{{Type: TypeAny, Object: "[Platform]"}},
			Raises:  true,
			Impl:    ImageParsePlatforms,
		},
		{
			Name:    "go_env",
			Doc:     "The GOOS and GOARCH that make go build target platform, for a build target's ctx.withEnv.",
			Args:    []Arg{{Name: "platform", Type: TypeAnyMap, Object: "Platform"}},
			Returns: []Ret{{Type: TypeStringMap}},
			Raises:  true,
			Impl:    ImageGoEnv,
		},
	},
}

// ImageAssemble writes the image spec describes as a layout at dest.
func ImageAssemble(ctx context.Context, spec types.ImageSpec, dest string) (types.ImageLayout, error) {
	if types.Tracing(ctx) {
		return types.ImageLayout{}, nil
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
	return manifestLayoutToTypes(l), nil
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
	allowTag := types.HasCharm(ctx, types.CharmUpdate)
	if ref.Digest == "" && !allowTag {
		return types.ImageLayout{}, fmt.Errorf("image.pull: %s names a tag, which can move: pin it as name@sha256:<hex>, or run the target under the update charm (magus run <target>:update) to resolve it", reference)
	}
	l, err := image.Pull(ctx, ref, dest, image.PullOptions{Client: imageClient(auth), Platform: platformFromTypes(platform), AllowTag: allowTag})
	if err != nil {
		return types.ImageLayout{}, err
	}
	return manifestLayoutToTypes(l), nil
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
	p, err := image.Push(ctx, layout, repo, image.PushOptions{Client: imageClient(auth), Tags: tags})
	if err != nil {
		return types.ImagePushed{}, err
	}
	return types.ImagePushed{
		Repository: p.Repository.String(),
		Digest:     digestToTypes(p.Digest),
		Tags:       p.Tags,
		Reference:  p.Reference().String(),
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
	for i, m := range p.Materials {
		d, err := digestFromTypes(m.Digest)
		if err != nil {
			return types.ImageDocument{}, fmt.Errorf("image.provenance: materials[%d]: %w", i, err)
		}
		prov.Materials = append(prov.Materials, image.Material{URI: m.URI, Digest: d})
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

// ImageOpen reads the layout at path back.
func ImageOpen(ctx context.Context, path string) (types.ImageLayout, error) {
	if types.Tracing(ctx) {
		return types.ImageLayout{}, nil
	}
	path = resolvePath(ctx, path)
	if err := imageCheck(ctx, []string{path}, nil); err != nil {
		return types.ImageLayout{}, fmt.Errorf("image.open: %w", err)
	}
	l, err := image.OpenLayout(ctx, path)
	if err != nil {
		return types.ImageLayout{}, err
	}
	if l.Descriptor.MediaType != ocispec.MediaTypeImageManifest {
		return layoutToTypes(l), nil
	}
	m, err := l.Manifest(ctx)
	if err != nil {
		return types.ImageLayout{}, fmt.Errorf("image.open: %w", err)
	}
	return manifestLayoutToTypes(m), nil
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

// ImageGoEnv is the GOOS/GOARCH environment for platform.
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
	return &oci.Client{Credentials: oci.Credentials{Username: auth.Username, Password: secret.NewValue(auth.Password.Value)}}
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
			Modes: image.ModePolicy(t.Modes), Symlinks: image.SymlinkPolicy(t.Symlinks), UID: t.UID, GID: t.GID,
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
	return image.Platform{OS: string(p.OS), Architecture: string(p.Architecture), Variant: string(p.Variant)}
}

func platformToTypes(p image.Platform) types.ImagePlatform {
	return types.ImagePlatform{OS: types.ImageOS(p.OS), Architecture: types.ImageArchitecture(p.Architecture), Variant: types.ImageVariant(p.Variant)}
}

func platformFromOCI(p *ocispec.Platform) image.Platform {
	if p == nil {
		return image.Platform{}
	}
	return image.Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant}
}

func digestToTypes(d digest.Digest) types.ImageDigest {
	if d == "" {
		return types.ImageDigest{}
	}
	return types.ImageDigest{Algorithm: d.Algorithm().String(), Encoded: d.Encoded()}
}

func digestFromTypes(d types.ImageDigest) (digest.Digest, error) {
	return image.ParseDigest(d.String())
}

func descriptorToTypes(d ocispec.Descriptor) types.ImageDescriptor {
	out := types.ImageDescriptor{
		MediaType:    types.ImageMediaType(d.MediaType),
		ArtifactType: types.ImageMediaType(d.ArtifactType),
		Digest:       digestToTypes(d.Digest),
		Size:         int(d.Size),
		Annotations:  d.Annotations,
	}
	if d.Platform != nil {
		p := platformToTypes(platformFromOCI(d.Platform))
		out.Platform = &p
	}
	return out
}

func descriptorFromTypes(d types.ImageDescriptor) (ocispec.Descriptor, error) {
	dg, err := digestFromTypes(d.Digest)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	return ocispec.Descriptor{MediaType: string(d.MediaType), ArtifactType: string(d.ArtifactType), Digest: dg, Size: int64(d.Size), Annotations: d.Annotations}, nil
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

func manifestLayoutToTypes(m image.ManifestLayout) types.ImageLayout {
	out := layoutToTypes(m.Layout)
	p := platformToTypes(m.Platform)
	cfg := descriptorToTypes(m.Config)
	out.Platform, out.Config, out.Layers, out.Created = &p, &cfg, descriptorsToTypes(m.Layers), m.Created
	if m.Added != nil {
		out.Added = &types.ImageAdded{
			Layer:       descriptorToTypes(m.Added.Layer),
			DiffID:      digestToTypes(m.Added.DiffID),
			Files:       m.Added.Files,
			Symlinks:    m.Added.Symlinks,
			Directories: m.Added.Directories,
		}
	}
	return out
}

func documentToTypes(d image.Document) types.ImageDocument {
	return types.ImageDocument{Path: d.Path, Descriptor: descriptorToTypes(d.Descriptor)}
}
