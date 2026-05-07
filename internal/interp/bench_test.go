package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

var largeMagefile = `
global function go_build(args: {string}) end
global function go_test(args: {string}) end
global function go_lint(args: {string}) end
global function go_vet(args: {string}) end
global function docker_build(args: {string}) end
global function docker_push(args: {string}) end
global function release_tag(args: {string}) end
global function release_sign(args: {string}) end
global function ci_lint(args: {string}) end
global function ci_test(args: {string}) end
global function db_migrate(args: {string}) end
global function db_seed(args: {string}) end
global function build(args: {string}) end
global function test(args: {string}) end
global function clean(args: {string}) end
`

func BenchmarkParse(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "magusfile.tl")
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
	path := filepath.Join(dir, "magusfile.tl")
	if err := os.WriteFile(path, []byte("global function noop(_args: {string}) end\n"), 0o644); err != nil {
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
