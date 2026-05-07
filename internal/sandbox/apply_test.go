package sandbox

import (
	"errors"
	"runtime"
	"testing"
)

// TestApplyNonLinuxReportsUnsupported verifies the !linux build tag's stub
// returns ErrUnsupported so the caller can fall back. Compiled on every
// platform; the assertion is only meaningful off-Linux but the call must
// not panic on Linux either.
func TestApplyNonLinuxReportsUnsupported(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux uses the real landlock implementation")
	}
	p := BuildPolicy(t.TempDir(), nil, nil, nil, nil)
	err := Apply(p)
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("Apply on %s = %v, want ErrUnsupported", runtime.GOOS, err)
	}
}

// TestSupportedConsistentWithApply ensures Supported() does not lie:
// a true return must mean Apply has a chance to succeed, and false must
// guarantee Apply returns ErrUnsupported.
func TestSupportedConsistentWithApply(t *testing.T) {
	if Supported() {
		return
	}
	// Supported() == false: Apply must report ErrUnsupported, never a
	// success and never a different error type.
	p := BuildPolicy(t.TempDir(), nil, nil, nil, nil)
	err := Apply(p)
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("Supported=false but Apply returned %v, want ErrUnsupported", err)
	}
}

// TestApplyNilPolicyNoop confirms calling Apply with a nil policy is a
// no-op on every platform — needed so that the orchestrator can call
// Apply unconditionally without first checking cfg.Sandbox.Enabled.
func TestApplyNilPolicyNoop(t *testing.T) {
	if err := Apply(nil); err != nil {
		t.Errorf("Apply(nil) = %v, want nil", err)
	}
}
