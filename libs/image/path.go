package image

import (
	"fmt"
	"path"
	"strings"
)

// HostFile is a regular file on the host that will be placed in an image.
type HostFile struct {
	Value string
	Base  string // directory Value is measured from; empty when Value is absolute
}

// HostDir is a host directory used as a source tree or path prefix.
type HostDir struct {
	Value string
	Base  string
}

// ImageFile is an absolute POSIX path of a file inside the image.
type ImageFile struct{ Value string }

// ImageDir is an absolute POSIX path of a directory inside the image.
type ImageDir struct{ Value string }

// Resolve joins Base and Value when Value is relative.
func (h HostFile) Resolve() string {
	if h.Value == "" || path.IsAbs(h.Value) || h.Base == "" {
		return path.Clean(h.Value)
	}
	return path.Join(h.Base, h.Value)
}

// Resolve joins Base and Value when Value is relative.
func (h HostDir) Resolve() string {
	if h.Value == "" || path.IsAbs(h.Value) || h.Base == "" {
		return path.Clean(h.Value)
	}
	return path.Join(h.Base, h.Value)
}

// Child returns an ImageFile under d.
func (d ImageDir) Child(rel string) (ImageFile, error) {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, "..") {
		return ImageFile{}, fmt.Errorf("image: image dir child %q: must be a relative path without ..", rel)
	}
	return ImageFile{Value: path.Clean(d.Value + "/" + rel)}, nil
}

// Place builds a File placement from a host file into an image path.
func (h HostFile) Place(dest ImageFile, mode Mode) File {
	return File{Source: h.Resolve(), Destination: dest.Value, Mode: mode.FileMode()}
}
