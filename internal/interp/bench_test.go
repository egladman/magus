package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

var largeMagefile = `
import "magus";
export fun go_build(args: [str]) > void {}
export fun go_test(args: [str]) > void {}
export fun go_lint(args: [str]) > void {}
export fun go_vet(args: [str]) > void {}
export fun docker_build(args: [str]) > void {}
export fun docker_push(args: [str]) > void {}
export fun release_tag(args: [str]) > void {}
export fun release_sign(args: [str]) > void {}
export fun ci_lint(args: [str]) > void {}
export fun ci_test(args: [str]) > void {}
export fun db_migrate(args: [str]) > void {}
export fun db_seed(args: [str]) > void {}
export fun build(args: [str]) > void {}
export fun test(args: [str]) > void {}
export fun clean(args: [str]) > void {}
`

func BenchmarkParse(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
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
	path := filepath.Join(dir, "magusfile.buzz")
	if err := os.WriteFile(path, []byte("import \"magus\";\nexport fun noop(args: [str]) > void {}\n"), 0o644); err != nil {
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
