package benched

import "testing"

func BenchmarkReuse(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Reuse()
	}
}
