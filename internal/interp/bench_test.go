package interp

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
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

// BenchmarkRunBuzzParallel runs independent magusfile targets across many
// projects concurrently. Each body does cwd-relative I/O (fs.writeFile), the very
// thing the process-global chdirMu serializes — so comparing -cpu=1 against -cpu=N
// quantifies how much real parallelism the chdir lock costs, and guards the
// chdirMu->ctx-cwd deserialization follow-up against regressing.
func BenchmarkRunBuzzParallel(b *testing.B) {
	const nProjects = 16
	ctx := context.Background()
	body := "import \"fs\";\nexport fun build(args: [str]) > void { fs.writeFile(\"out.txt\", \"x\"); }\n"

	type proj struct {
		src *Source
		dir string
	}
	projects := make([]proj, nProjects)
	for i := range projects {
		dir := b.TempDir()
		path := filepath.Join(dir, "magusfile.buzz")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
		projects[i] = proj{src: &Source{Dir: dir, Files: []string{path}, Engine: "buzz"}, dir: dir}
	}

	var ctr atomic.Int64
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			p := projects[int(ctr.Add(1))%nProjects]
			if err := Run(ctx, p.src, "build", nil, p.dir); err != nil {
				b.Fatal(err)
			}
		}
	})
}

