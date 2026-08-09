// Two build suffixes in a row, so trimBuildSuffixes must loop rather than strip
// once. Pairs with bytes.go after trimming, and must stay silent.
package sprawl

import "testing"

func TestBytesJSWasm(t *testing.T) { _ = Resolve("a") }
