//go:build !linux

package sandbox

// ABI returns ErrUnsupported on every non-Linux host.
func ABI() (int, error) {
	return 0, ErrUnsupported
}
