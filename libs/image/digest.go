package image

import (
	"context"
	"io"

	"github.com/opencontainers/go-digest"

	"github.com/egladman/magus/libs/oci"
)

func checkDigest(d digest.Digest) error {
	_, err := oci.ParseDigest(d.String())
	return err
}

// ctxReader stops a long blob copy when ctx is done. libs/oci keeps its own for
// VerifyBlob; exporting one would add API for a three-line type.
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
