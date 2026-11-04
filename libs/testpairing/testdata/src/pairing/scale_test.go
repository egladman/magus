// cross-cutting: asserts one budget across every operation at once

// The marker is a comment like any other and excuses nothing.
package pairing // want `scale_test.go has no source file of the same name`

import "testing"

func TestScale(t *testing.T) { _ = Resolve("a") }
