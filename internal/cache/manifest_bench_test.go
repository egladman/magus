package cache

import "testing"

// BenchmarkFlattenPath measures path flattening, called on every manifest/log/
// remote path construction (per target, per cache op).
func BenchmarkFlattenPath(b *testing.B) {
	const p = "services/api/gateway/internal/handlers"
	b.ReportAllocs()
	for b.Loop() {
		_ = flattenPath(p)
	}
}
