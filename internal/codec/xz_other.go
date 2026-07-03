//go:build !cgo || wasm

package codec

// The `|| wasm` arm routes the wasm playground here even under cgo: native
// liblzma can't be linked into a wasm sandbox (see xz_cgo.go).

import (
	"io"

	"github.com/ulikunitz/xz"
)

// newXzReader returns a streaming xz decompressor reading from r.
func newXzReader(r io.Reader) (io.ReadCloser, error) {
	xr, err := xz.NewReader(r)
	if err != nil {
		return nil, err
	}
	// ulikunitz/xz Reader does not implement io.Closer; wrap it.
	return io.NopCloser(xr), nil
}
