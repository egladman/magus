//go:build !wasm

package std

import (
	"context"
	"errors"
	"fmt"

	"github.com/egladman/magus/internal/image"

	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module image -lang buzz -out ../internal/interp/bindings/gen/image.go -pkg bindgen

// Image is the Buzz image module: it builds OCI image layouts from host files
// without a daemon or a Dockerfile, and moves them to and from a registry.
var Image = Module{
	Name: "image",
	Doc:  "Build, assemble and publish OCI images. Layouts are directories holding exactly one manifest or index; equal inputs give byte-identical layouts.",
	Methods: []Method{
		{
			Name: "pull",
			Doc:  "Fetch the image at reference, a name@sha256:<hex> digest reference, for platform into the OCI layout directory dest. Returns the layout.",
			Args: []Arg{
				{Name: "reference", Type: TypeString},
				{Name: "platform", Type: TypeAnyMap, Object: "ImagePlatform"},
				{Name: "dest", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ImageLayout"}},
			Raises:  true,
			Impl:    ImagePull,
		},
		{
			Name: "write",
			Doc:  "Write the image described by spec, at most one new layer over spec.base, into the OCI layout directory dest. Returns the layout.",
			Args: []Arg{
				{Name: "spec", Type: TypeAnyMap, Object: "ImageSpec"},
				{Name: "dest", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ImageLayout"}},
			Raises:  true,
			Impl:    ImageWrite,
		},
		{
			Name: "index",
			Doc:  "Combine layouts, each holding one platform manifest, into an image index written to the OCI layout directory dest. Returns the layout.",
			Args: []Arg{
				{Name: "layouts", Type: TypeStringSlice},
				{Name: "dest", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ImageLayout"}},
			Raises:  true,
			Impl:    ImageIndex,
		},
		{
			Name: "publish",
			Doc:  "Push the layout to repository, then tag it with each of tags; an empty tags list pushes by digest only. Returns what the registry confirmed.",
			Args: []Arg{
				{Name: "layout", Type: TypeString},
				{Name: "repository", Type: TypeString},
				{Name: "tags", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ImagePublished"}},
			Raises:  true,
			Impl:    ImagePublish,
		},
	},
}

// ImagePull fetches the image named by a digest reference into an OCI layout
// at dest. A tag reference is an error; it does not relabel the platform.
func ImagePull(_ context.Context, _ string, platform map[string]any, _ string) (types.ImageLayout, error) {
	p, err := image.DecodePlatform(platform)
	if err != nil {
		return types.ImageLayout{}, fmt.Errorf("image.pull: %w", err)
	}
	if err := p.Validate(); err != nil {
		return types.ImageLayout{}, err
	}
	return types.ImageLayout{}, errors.New("image.pull: not built yet")
}

// ImageWrite assembles spec into an OCI layout at dest, adding at most one
// layer. A failed write leaves dest untouched.
func ImageWrite(_ context.Context, spec map[string]any, _ string) (types.ImageLayout, error) {
	s, err := image.DecodeSpec(spec)
	if err != nil {
		return types.ImageLayout{}, err
	}
	if err := s.Validate(); err != nil {
		return types.ImageLayout{}, err
	}
	return types.ImageLayout{}, errors.New("image.write: not built yet")
}

// ImageIndex joins single-platform layouts into one image index at dest.
// Two layouts for the same platform are an error.
func ImageIndex(_ context.Context, _ []string, _ string) (types.ImageLayout, error) {
	return types.ImageLayout{}, errors.New("image.index: not built yet")
}

// ImagePublish pushes a layout to repository under each of tags and reports
// the digest. It refuses when MAGUS_OFFLINE is set.
func ImagePublish(_ context.Context, _, _ string, _ []string, _ map[string]any) (types.ImagePublished, error) {
	return types.ImagePublished{}, errors.New("image.publish: not built yet")
}
