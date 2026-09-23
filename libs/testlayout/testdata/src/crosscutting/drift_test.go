// Unmarked and unpaired: reported, and the message names the marker as the way out.
package crosscutting // want `drift_test.go has no source file of the same name; .* .// cross-cutting: <why>.`

import "testing"

// cross-cutting: below the package clause, so it does not count
func TestDrift(t *testing.T) { _ = Widget{} }
