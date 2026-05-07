package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// FuzzParse verifies that Parse never panics on arbitrary Teal source content.
// The invariant: any bytes written as a .tl file must produce either
// (targets, nil) or (nil, error) — never a panic.
func FuzzParse(f *testing.F) {
	f.Add([]byte("global function build(_args: {string}) end\n"))
	f.Add([]byte("global function build(_args: {string}) end\nglobal function test(_args: {string}) end\n"))
	f.Add([]byte("local x: string = {"))
	f.Add([]byte(""))
	f.Add([]byte("\x00\x01\x02\x03"))
	f.Add([]byte("global function noop(_args: {string}) end\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, "magusfile.tl")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		src := &Source{Dir: dir, Files: []string{path}}
		_, _ = Parse(context.Background(), src)
	})
}
