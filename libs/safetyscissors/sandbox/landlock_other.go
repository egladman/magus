//go:build !linux

package sandbox

// ABI is an *UnsupportedError on every host but Linux: landlock is Linux's.
func ABI() (int, error) {
	return 0, &UnsupportedError{}
}
