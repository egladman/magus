// Pairs with relay_unix.go, so only its name is reported, with the stem the source has.
package strict // want `relay_unix_test.go is named for unix, .* relay_linux.go and relay_darwin.go`

import "testing"

func TestRelay(t *testing.T) { _ = relay() }
