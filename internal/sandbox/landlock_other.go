//go:build !linux

package sandbox

// Apply on non-Linux hosts returns ErrUnsupported. Callers must fall back to
// the binding-level policy checks (which run in pure Go and do not depend on
// kernel support) and emit MGS2005 so the operator knows kernel enforcement
// is absent.
func Apply(p *Policy) error {
	if p == nil {
		return nil
	}
	return ErrUnsupported
}

// Supported reports false on every non-Linux host.
func Supported() bool {
	return false
}
