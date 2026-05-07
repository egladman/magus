//go:build !linux && !darwin

package reflink

import (
	"io"
	"os"
)

// Probe always returns false on non-Linux/Darwin platforms.
func Probe(_ string) bool { return false }

// Clone copies src to dst using io.Copy. On platforms other than Linux
// and Darwin there is no kernel-level CoW mechanism accessible from Go
// without cgo, so we fall straight to a userspace copy.
func Clone(src, dst string) error {
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
