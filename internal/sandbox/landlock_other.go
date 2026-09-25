//go:build !linux

package sandbox

import "github.com/egladman/magus/internal/sandbox/filesystem"

// ABI returns ErrUnsupported on every non-Linux host.
func ABI() (int, error) {
	return 0, ErrUnsupported
}

func restrictProcess([]filesystem.Rule) error {
	return ErrUnsupported
}
