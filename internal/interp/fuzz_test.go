package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// FuzzParse verifies that Parse never panics on arbitrary Buzz source content.
// The invariant: any bytes written as a .buzz file must produce either
// (targets, nil) or (nil, error) — never a panic.
func FuzzParse(f *testing.F) {
	f.Add([]byte("import \"magus\";\nexport fun build(_args: [str]) > void {}\n"))
	f.Add([]byte("import \"magus\";\nexport fun build(_args: [str]) > void {}\nexport fun test(_args: [str]) > void {}\n"))
	f.Add([]byte("var x: str = {"))
	f.Add([]byte(""))
	f.Add([]byte("\x00\x01\x02\x03"))
	f.Add([]byte("import \"magus\";\nexport fun noop(_args: [str]) > void {}\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, "magusfile.buzz")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		src := &Source{Dir: dir, Files: []string{path}}
		_, _ = Parse(context.Background(), src)
	})
}
