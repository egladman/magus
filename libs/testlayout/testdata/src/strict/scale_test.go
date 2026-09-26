// cross-cutting: asserts one budget across every widget operation at once

// The marker no longer counts, and the message names the allow list instead of it.
package strict // want `scale_test.go has no source file of the same name; .* add it to the allow list`

import "testing"

func TestScale(t *testing.T) { _ = Widget{} }
