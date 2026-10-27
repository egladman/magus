package image

import (
	"context"
	"fmt"
	"io"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/egladman/magus/libs/oci"
)

// ParseDigest reads "sha256:<hex>" and refuses a tag, bare hex, or another algorithm:
// sha256 is what a layout's blobs/ directory and a registry both pin.
func ParseDigest(s string) (digest.Digest, error) {
	d := digest.Digest(s)
	if err := checkDigest(d); err != nil {
		return "", fmt.Errorf("digest %q: %w", s, err)
	}
	return d, nil
}

func checkDigest(d digest.Digest) error {
	if err := d.Validate(); err != nil {
		return err
	}
	if d.Algorithm() != digest.SHA256 {
		return fmt.Errorf("algorithm %s, want sha256", d.Algorithm())
	}
	return nil
}

// Verify reads r to its end and reports whether it is exactly desc.Size bytes hashing
// to desc.Digest. A mismatch wraps oci.ErrBlobDigest; a short or long stream says by how
// much.
func Verify(ctx context.Context, r io.Reader, desc ocispec.Descriptor) error {
	if err := checkDigest(desc.Digest); err != nil {
		return err
	}
	v := desc.Digest.Verifier()
	// One byte past the size shows an overlong stream without reading all of it.
	n, err := io.Copy(v, io.LimitReader(&ctxReader{ctx: ctx, r: r}, desc.Size+1))
	if err != nil {
		return err
	}
	if n != desc.Size {
		return fmt.Errorf("size is %d bytes, descriptor says %d", n, desc.Size)
	}
	if !v.Verified() {
		return fmt.Errorf("%s: %w", desc.Digest, oci.ErrBlobDigest)
	}
	return nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
