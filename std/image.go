//go:build !wasm

package std

import (
	"context"
	"fmt"

	"github.com/egladman/magus/libs/image"

	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module image -lang buzz -out ../internal/interp/bindings/gen/image.go -pkg bindgen

// Image is the Buzz image module: it builds OCI image layouts from host files
// without a daemon or a Dockerfile, and moves them to and from a registry.
var Image = Module{
	Name: "image",
	Doc:  "Assemble and publish OCI images. An Image is the artifact; a Container is a running instance. Layouts hold one Image Manifest or Image Index.",
	Methods: []Method{
		{
			Name: "pullLayout",
			Doc:  "Fetch the image at reference (name@sha256:<hex>) for platform into dest as an OCI Image Layout. Optional auth: {username, password:{value}}.",
			Args: []Arg{
				{Name: "reference", Type: TypeString},
				{Name: "platform", Type: TypeAnyMap, Object: "ImagePlatform"},
				{Name: "dest", Type: TypeString},
				{Name: "auth", Type: TypeAnyMap, Object: "ImageRegistryAuth", Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ImageLayout"}},
			Raises:  true,
			Impl:    ImagePullLayout,
		},
		{
			Name: "assembleLayout",
			Doc:  "Assemble an OCI Image Layout from spec into dest: optional base, at most one new Layer for files/symlinks, then config and Image Manifest.",
			Args: []Arg{
				{Name: "spec", Type: TypeAnyMap, Object: "ImageSpec"},
				{Name: "dest", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ImageLayout"}},
			Raises:  true,
			Impl:    ImageAssembleLayout,
		},
		{
			Name: "assembleIndex",
			Doc:  "Combine single-platform layouts into an Image Index at dest.",
			Args: []Arg{
				{Name: "layouts", Type: TypeStringSlice},
				{Name: "dest", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ImageLayout"}},
			Raises:  true,
			Impl:    ImageAssembleIndex,
		},
		{
			Name: "publishLayout",
			Doc:  "Push the layout to repository under tags; empty tags means digest only. Optional auth: {username, password:{value}}.",
			Args: []Arg{
				{Name: "layout", Type: TypeString},
				{Name: "repository", Type: TypeString},
				{Name: "tags", Type: TypeStringSlice},
				{Name: "auth", Type: TypeAnyMap, Object: "ImageRegistryAuth", Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ImagePublished"}},
			Raises:  true,
			Impl:    ImagePublishLayout,
		},
		{
			Name: "writeSbom",
			Doc:  "Write an SPDX SBOM for layout to dest and return the document descriptor.",
			Args: []Arg{
				{Name: "layout", Type: TypeString},
				{Name: "dest", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ImageSBOM"}},
			Raises:  true,
			Impl:    ImageWriteSbom,
		},
		{
			Name: "writeProvenance",
			Doc:  "Write an in-toto provenance statement for layout using spec to dest.",
			Args: []Arg{
				{Name: "layout", Type: TypeString},
				{Name: "spec", Type: TypeAnyMap, Object: "ImageProvenanceSpec"},
				{Name: "dest", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ImageProvenance"}},
			Raises:  true,
			Impl:    ImageWriteProvenance,
		},
		{
			Name: "attachArtifacts",
			Doc:  "Attach optional sbomPath and provenancePath documents onto the Image Index at layout.",
			Args: []Arg{
				{Name: "layout", Type: TypeString},
				{Name: "sbomPath", Type: TypeString, Optional: true},
				{Name: "provenancePath", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ImageLayout"}},
			Raises:  true,
			Impl:    ImageAttachArtifacts,
		},
	},
}

func layoutToTypes(l image.Layout) types.ImageLayout {
	return types.ImageLayout{
		Path:      l.Path,
		Digest:    digestToTypes(image.MustDigest(l.Descriptor.Digest)),
		MediaType: l.Descriptor.MediaType,
		Size:      int(l.Descriptor.Size),
		Platform: types.ImagePlatform{
			OS: l.Platform.OS, Architecture: l.Platform.Architecture, Variant: l.Platform.Variant,
		},
		Op:           string(l.Op),
		Base:         l.Base,
		BaseDigest:   digestToTypes(l.BaseDigest),
		FileCount:    l.FileCount,
		SymlinkCount: l.SymlinkCount,
		Layer:        layerToTypes(l.Layer),
		ConfigDigest: digestToTypes(l.ConfigDigest),
		Created:      l.Created,
	}
}

func digestToTypes(d image.Digest) types.ImageDigest {
	if d.IsZero() {
		return types.ImageDigest{}
	}
	return types.ImageDigest{Algorithm: d.Algorithm(), Encoded: d.Encoded()}
}

func diffIDToTypes(d image.DiffID) types.ImageDigest {
	return digestToTypes(d.Digest())
}

func layerToTypes(l image.Layer) types.ImageLayer {
	if l.IsZero() {
		return types.ImageLayer{}
	}
	return types.ImageLayer{
		Digest: digestToTypes(l.Digest), DiffID: diffIDToTypes(l.DiffID),
		Size: int(l.Size), MediaType: l.MediaType,
	}
}

func decodeAuth(m map[string]any) (*image.RegistryAuth, error) {
	if m == nil {
		return nil, nil
	}
	auth := &image.RegistryAuth{}
	if u, ok := m["username"].(string); ok {
		auth.Username = u
	}
	switch p := m["password"].(type) {
	case nil:
	case string:
		return nil, fmt.Errorf("image: auth.password: want Secret object {value}, got string")
	case map[string]any:
		v, _ := p["value"].(string)
		auth.Password = image.NewSecret(v)
	default:
		return nil, fmt.Errorf("image: auth.password: want Secret object, got %T", p)
	}
	return auth, nil
}

// ImagePullLayout fetches a digest-pinned image into dest.
func ImagePullLayout(ctx context.Context, reference string, platform map[string]any, dest string, auth map[string]any) (types.ImageLayout, error) {
	p, err := image.DecodePlatform(platform)
	if err != nil {
		return types.ImageLayout{}, fmt.Errorf("image.pull: %w", err)
	}
	a, err := decodeAuth(auth)
	if err != nil {
		return types.ImageLayout{}, err
	}
	l, err := image.PullLayout(ctx, reference, p, dest, a)
	if err != nil {
		return types.ImageLayout{}, err
	}
	return layoutToTypes(l), nil
}

// ImageAssembleLayout builds an OCI Image Layout from spec at dest.
func ImageAssembleLayout(ctx context.Context, spec map[string]any, dest string) (types.ImageLayout, error) {
	s, err := image.DecodeSpec(spec)
	if err != nil {
		return types.ImageLayout{}, err
	}
	l, err := image.AssembleLayout(ctx, s, dest)
	if err != nil {
		return types.ImageLayout{}, err
	}
	return layoutToTypes(l), nil
}

// ImageAssembleIndex joins single-platform layouts into an Image Index.
func ImageAssembleIndex(ctx context.Context, layouts []string, dest string) (types.ImageLayout, error) {
	l, err := image.AssembleIndex(ctx, layouts, dest)
	if err != nil {
		return types.ImageLayout{}, err
	}
	return layoutToTypes(l), nil
}

// ImagePublishLayout pushes a layout to a repository.
func ImagePublishLayout(ctx context.Context, layout, repository string, tags []string, auth map[string]any) (types.ImagePublished, error) {
	a, err := decodeAuth(auth)
	if err != nil {
		return types.ImagePublished{}, err
	}
	p, err := image.PublishLayout(ctx, layout, repository, tags, a)
	if err != nil {
		return types.ImagePublished{}, err
	}
	return types.ImagePublished{
		Repository: p.Repository, Digest: digestToTypes(p.Digest), Tags: p.Tags,
		Op: string(p.Op),
	}, nil
}

// ImageWriteSbom writes an SPDX document for the layout.
func ImageWriteSbom(ctx context.Context, layoutPath, dest string) (types.ImageSBOM, error) {
	l, err := image.OpenLayout(layoutPath)
	if err != nil {
		return types.ImageSBOM{}, fmt.Errorf("image.sbom: %w", err)
	}
	doc, err := image.WriteSBOM(ctx, l, dest)
	if err != nil {
		return types.ImageSBOM{}, err
	}
	return types.ImageSBOM{Path: doc.Path, MediaType: doc.MediaType, Digest: digestToTypes(doc.Digest)}, nil
}

// ImageWriteProvenance writes an in-toto provenance statement.
func ImageWriteProvenance(ctx context.Context, layoutPath string, spec map[string]any, dest string) (types.ImageProvenance, error) {
	l, err := image.OpenLayout(layoutPath)
	if err != nil {
		return types.ImageProvenance{}, fmt.Errorf("image.provenance: %w", err)
	}
	ps := image.ProvenanceSpec{}
	if v, ok := spec["sourceRevision"].(string); ok {
		ps.SourceRevision = v
	}
	if v, ok := spec["builderId"].(string); ok {
		ps.BuilderID = v
	}
	doc, err := image.WriteProvenance(ctx, l, ps, dest)
	if err != nil {
		return types.ImageProvenance{}, err
	}
	return types.ImageProvenance{Path: doc.Path, MediaType: doc.MediaType, Digest: digestToTypes(doc.Digest)}, nil
}

// ImageAttachArtifacts binds SBOM/provenance files onto an Image Index layout.
func ImageAttachArtifacts(ctx context.Context, layoutPath, sbomPath, provenancePath string) (types.ImageLayout, error) {
	l, err := image.OpenLayout(layoutPath)
	if err != nil {
		return types.ImageLayout{}, fmt.Errorf("image.attach: %w", err)
	}
	var sbom *image.SBOMDoc
	var prov *image.ProvenanceDoc
	if sbomPath != "" {
		sbom = &image.SBOMDoc{Path: sbomPath}
	}
	if provenancePath != "" {
		prov = &image.ProvenanceDoc{Path: provenancePath}
	}
	out, err := image.AttachArtifacts(ctx, l, sbom, prov)
	if err != nil {
		return types.ImageLayout{}, err
	}
	return layoutToTypes(out), nil
}
