//go:build !wasm

package std

import (
	"context"
	"errors"

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

// ImagePull is image.pull.
func ImagePull(ctx context.Context, reference string, platform map[string]any, dest string) (types.ImageLayout, error) {
	return types.ImageLayout{}, errors.New("image.pull: not built yet")
}

// ImageWrite is image.write.
func ImageWrite(ctx context.Context, spec map[string]any, dest string) (types.ImageLayout, error) {
	return types.ImageLayout{}, errors.New("image.write: not built yet")
}

// ImageIndex is image.index.
func ImageIndex(ctx context.Context, layouts []string, dest string) (types.ImageLayout, error) {
	return types.ImageLayout{}, errors.New("image.index: not built yet")
}

// ImagePublish is image.publish.
func ImagePublish(ctx context.Context, layout, repository string, tags []string, opts map[string]any) (types.ImagePublished, error) {
	return types.ImagePublished{}, errors.New("image.publish: not built yet")
}
