//go:build !linux && !darwin

package reflink

import (
	"io"
	"os"
)

// probe always returns false on non-Linux/Darwin platforms.
func probe(_ string) bool { return false }

// clone copies src to dst using io.Copy. On platforms other than Linux
// and Darwin there is no kernel-level CoW mechanism accessible from Go
// without cgo, so we fall straight to a userspace copy.
func clone(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	// Propagate Close: ENOSPC on writeback flush is otherwise invisible, and a
	// truncated copy would be recorded as a successful clone and cached as valid.
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
	}()

	_, err = io.Copy(out, in)
	return err
}
