//go:build !linux && !darwin

package endpoint

// unixName is path as given; the platform's own bind or connect reports one too long.
func unixName(path string) (string, func(), error) { return path, func() {}, nil }
