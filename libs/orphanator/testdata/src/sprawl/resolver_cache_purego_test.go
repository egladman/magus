// Pairs exactly with resolver_cache_purego.go, so it must stay silent.
package sprawl

import "testing"

func TestResolveCachedPureGo(t *testing.T) { _ = Resolve("a") }
