package image

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/internal/file"
)

const (
	layoutMarker  = `{"imageLayoutVersion":"1.0.0"}`
	layoutVersion = "1.0.0"
)

// blob is one content-addressed file bound for blobs/sha256. The store re-hashes
// what open returns, so a caller's wrong digest or size fails the write.
type blob struct {
	desc ocispec.Descriptor
	open func() (io.ReadCloser, error)
}

func bytesBlob(mediaType string, data []byte) blob {
	return blob{
		desc: ocispec.Descriptor{MediaType: mediaType, Digest: digest.FromBytes(data), Size: int64(len(data))},
		open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil },
	}
}

// layoutBlob reads desc from the layout at path; writeLayout verifies it.
func layoutBlob(path string, desc ocispec.Descriptor) blob {
	return blob{
		desc: desc,
		open: func() (io.ReadCloser, error) { return os.Open(blobPath(path, desc.Digest)) },
	}
}

func blobPath(layout string, d digest.Digest) string {
	return filepath.Join(layout, "blobs", d.Algorithm().String(), d.Encoded())
}

func checkSHA256(d digest.Digest) bool {
	return d.Validate() == nil && d.Algorithm() == digest.SHA256
}

// writeLayout lays out blobs plus an index.json naming root at dest (R21, R22).
// root's blob must be among blobs. Nothing is visible at dest until every blob
// is written and verified, and on any error dest is left as it was. The sandbox
// check on dest and on its parent, where staging lives, belongs to the caller.
func writeLayout(ctx context.Context, dest string, root ocispec.Descriptor, blobs []blob) (err error) {
	if !checkSHA256(root.Digest) {
		return fmt.Errorf("image.write: layout: root digest %q is not a sha256 digest", root.Digest)
	}
	rootHeld := false
	for _, b := range blobs {
		if b.desc.Digest == root.Digest {
			rootHeld = true
		}
	}
	if !rootHeld {
		return fmt.Errorf("image.write: layout: root %s is not among the blobs", root.Digest)
	}
	index, err := json.Marshal(ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		Manifests: []ocispec.Descriptor{root},
	})
	if err != nil {
		return fmt.Errorf("image.write: layout: index.json: %w", err)
	}

	dest = filepath.Clean(dest)
	staging, err := os.MkdirTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".staging-*")
	if err != nil {
		return fmt.Errorf("image.write: layout: staging directory: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(staging)
		}
	}()

	blobDir := filepath.Join(staging, "blobs", digest.SHA256.String())
	// MkdirTemp makes 0700 and MkdirAll honors the umask, so chmod spells the mode (R31).
	for _, dir := range []string{staging, filepath.Join(staging, "blobs"), blobDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("image.write: layout: %w", err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			return fmt.Errorf("image.write: layout: %w", err)
		}
	}
	if err := file.WriteFileAtomic(filepath.Join(staging, "oci-layout"), []byte(layoutMarker), 0o644); err != nil {
		return fmt.Errorf("image.write: layout: oci-layout: %w", err)
	}
	if err := file.WriteFileAtomic(filepath.Join(staging, "index.json"), index, 0o644); err != nil {
		return fmt.Errorf("image.write: layout: index.json: %w", err)
	}
	written := map[digest.Digest]bool{}
	for _, b := range blobs {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("image.write: layout: %w", err)
		}
		if !checkSHA256(b.desc.Digest) {
			return fmt.Errorf("image.write: layout: blob digest %q is not a sha256 digest", b.desc.Digest)
		}
		if written[b.desc.Digest] {
			continue
		}
		if err := writeBlob(filepath.Join(blobDir, b.desc.Digest.Encoded()), b); err != nil {
			return err
		}
		written[b.desc.Digest] = true
	}
	if err := syncDir(blobDir); err != nil {
		return fmt.Errorf("image.write: layout: %w", err)
	}
	return replaceDir(staging, dest)
}

func writeBlob(target string, b blob) error {
	src, err := b.open()
	if err != nil {
		return fmt.Errorf("image.write: layout: blob %s: %w", b.desc.Digest, err)
	}
	defer src.Close()
	v := &verifier{want: b.desc, digester: digest.SHA256.Digester()}
	if err := file.WriteFrom(target, io.TeeReader(src, v), 0o644); err != nil {
		return fmt.Errorf("image.write: layout: blob %s: %w", b.desc.Digest, err)
	}
	if err := v.check(); err != nil {
		return fmt.Errorf("image.write: layout: blob %s: %w", b.desc.Digest, err)
	}
	return nil
}

// verifier counts and hashes what is written through it.
type verifier struct {
	want     ocispec.Descriptor
	digester digest.Digester
	n        int64
}

func (v *verifier) Write(p []byte) (int, error) {
	v.n += int64(len(p))
	return v.digester.Hash().Write(p)
}

func (v *verifier) check() error {
	if v.n != v.want.Size {
		return fmt.Errorf("size is %d bytes, descriptor says %d", v.n, v.want.Size)
	}
	if got := v.digester.Digest(); got != v.want.Digest {
		return fmt.Errorf("content hashes to %s, descriptor says %s", got, v.want.Digest)
	}
	return nil
}

// replaceDir swaps staging in for dest: dest moves aside, staging takes its
// place, and the old tree is removed last. If installing fails the old tree
// goes back.
func replaceDir(staging, dest string) error {
	aside := staging + ".old"
	hadOld := true
	if err := os.Rename(dest, aside); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("image.write: layout: move %s aside: %w", dest, err)
		}
		hadOld = false
	}
	if err := os.Rename(staging, dest); err != nil {
		if hadOld {
			_ = os.Rename(aside, dest)
		}
		return fmt.Errorf("image.write: layout: install %s: %w", dest, err)
	}
	if hadOld {
		if err := os.RemoveAll(aside); err != nil {
			return fmt.Errorf("image.write: layout: %s is installed but the old tree %s remains: %w", dest, aside, err)
		}
	}
	return nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Open reads the layout at path. It refuses one that does not hold exactly one
// descriptor and one whose root blob fails its digest or size (R23).
func Open(path string) (Layout, error) {
	marker, err := os.ReadFile(filepath.Join(path, "oci-layout"))
	if err != nil {
		return Layout{}, fmt.Errorf("image.open: %s: %w", path, err)
	}
	var m ocispec.ImageLayout
	if err := json.Unmarshal(marker, &m); err != nil {
		return Layout{}, fmt.Errorf("image.open: %s: oci-layout: %w", path, err)
	}
	if m.Version != layoutVersion {
		return Layout{}, fmt.Errorf("image.open: %s: oci-layout version %q, want %q", path, m.Version, layoutVersion)
	}
	raw, err := os.ReadFile(filepath.Join(path, "index.json"))
	if err != nil {
		return Layout{}, fmt.Errorf("image.open: %s: %w", path, err)
	}
	var idx ocispec.Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return Layout{}, fmt.Errorf("image.open: %s: index.json: %w", path, err)
	}
	if len(idx.Manifests) != 1 {
		return Layout{}, fmt.Errorf("image.open: %s: index.json holds %d descriptors, want exactly 1", path, len(idx.Manifests))
	}
	desc := idx.Manifests[0]
	if _, err := readBlob(path, desc); err != nil {
		return Layout{}, err
	}
	l := Layout{Path: path, Descriptor: desc}
	if p := desc.Platform; p != nil {
		l.Platform = Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant}
	}
	return l, nil
}

// readBlob returns desc's content from the layout at path, verified against
// its digest and size.
func readBlob(path string, desc ocispec.Descriptor) ([]byte, error) {
	if !checkSHA256(desc.Digest) {
		return nil, fmt.Errorf("image.open: %s: blob digest %q is not a sha256 digest", path, desc.Digest)
	}
	f, err := os.Open(blobPath(path, desc.Digest))
	if err != nil {
		return nil, fmt.Errorf("image.open: %s: blob %s: %w", path, desc.Digest, err)
	}
	defer f.Close()
	v := &verifier{want: desc, digester: digest.SHA256.Digester()}
	// One byte past the descriptor's size shows a size mismatch without reading
	// an arbitrarily large corrupted file.
	data, err := io.ReadAll(io.TeeReader(io.LimitReader(f, desc.Size+1), v))
	if err != nil {
		return nil, fmt.Errorf("image.open: %s: blob %s: %w", path, desc.Digest, err)
	}
	if err := v.check(); err != nil {
		return nil, fmt.Errorf("image.open: %s: blob %s: %w", path, desc.Digest, err)
	}
	return data, nil
}
