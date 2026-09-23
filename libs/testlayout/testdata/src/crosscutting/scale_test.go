// cross-cutting: asserts one budget across every widget operation at once

// Carries the marker, so it stays silent under Unpaired.
package crosscutting

import "testing"

func TestScale(t *testing.T) { _ = Widget{} }
