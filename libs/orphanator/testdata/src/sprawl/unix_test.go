// The whole base is a build suffix, so trimming reduces it to the empty string.
// Nothing pairs and nothing narrows, so the default rule stays silent.
package sprawl

import "testing"

func TestUnixOnly(t *testing.T) { _ = Resolve("a") }
