// A conventional benchmark file, exempt regardless of what it pairs with.
package sprawl

import "testing"

func BenchmarkResolve(b *testing.B) {
	for b.Loop() {
		Resolve("a")
	}
}
