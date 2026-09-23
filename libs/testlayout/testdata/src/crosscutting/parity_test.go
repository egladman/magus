// cross-cutting:

// The marker names no reason, which does not count.
package crosscutting // want `parity_test.go has no source file of the same name`

import "testing"

func TestParity(t *testing.T) { _ = Widget{} }
