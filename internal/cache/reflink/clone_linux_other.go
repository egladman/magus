//go:build linux && !amd64 && !arm64

// clone_linux_other.go is the 32-bit Linux fallback. The reflink fast paths
// in clone_linux.go convert int64 file sizes to int for copy_file_range,
// which is unsafe on 32-bit. Tack does not ship 32-bit Linux binaries; this
// file exists so the package builds on those targets and falls back to
// userspace io.Copy.

package reflink

import (
	"io"
	"os"
)

// probe always returns false on 32-bit Linux; no CoW fast path is available.
func probe(_ string) bool { return false }

func clone(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
