//go:build !linux && !darwin

package pipepeer

import (
	"errors"
	"os"
	"testing"
)

// TestPipeProofUnsupported pins what a platform without proof does: every question
// answers ErrUnsupported or false, so no caller ever waits on an unproven peer.
func TestPipeProofUnsupported(t *testing.T) {
	if _, err := ReadEnd(os.Getpid(), 0); !errors.Is(err, ErrUnsupported) {
		t.Errorf("ReadEnd error = %v, want ErrUnsupported", err)
	}
	if _, err := (Pipe{}).Writers(); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Writers error = %v, want ErrUnsupported", err)
	}
	if (Pipe{}).WrittenBy(os.Getpid()) || SameExecutable(os.Getpid()) {
		t.Errorf("an unsupported platform proved a peer")
	}
}
