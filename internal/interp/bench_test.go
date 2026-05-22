package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

var largeMagefile = `
import "magus";
export fun go_build(_args: [str]) > void {}
export fun go_test(_args: [str]) > void {}
export fun go_lint(_args: [str]) > void {}
export fun go_vet(_args: [str]) > void {}
export fun docker_build(_args: [str]) > void {}
export fun docker_push(_args: [str]) > void {}
export fun release_tag(_args: [str]) > void {}
export fun release_sign(_args: [str]) > void {}
export fun ci_lint(_args: [str]) > void {}
export fun ci_test(_args: [str]) > void {}
export fun db_migrate(_args: [str]) > void {}
export fun db_seed(_args: [str]) > void {}
export fun build(_args: [str]) > void {}
export fun test(_args: [str]) > void {}
export fun clean(_args: [str]) > void {}
`

func BenchmarkParse(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte(largeMagefile), 0o644); err != nil {
		b.Fatal(err)
	}
	src := &Source{Dir: dir, Files: []string{path}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Parse(context.Background(), src)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFind(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "magusfile.bzz")
	if err := os.WriteFile(path, []byte("import \"magus\";\nexport fun noop(_args: [str]) > void {}\n"), 0o644); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Find(dir)
		if err != nil {
			b.Fatal(err)
		}
	}
}
