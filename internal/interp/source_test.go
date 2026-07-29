package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindMagusBzz(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte("import \"magus\";\nexport fun build(ctx: magus\\Context, args: [str]) > void {}\n"), 0o644))

	src, err := Find(dir)
	require.NoError(t, err)
	require.NotNil(t, src, "expected source, got nil")
	assert.Equal(t, dir, src.Dir)
	assert.Len(t, src.Files, 1)
}

func TestFindNothing(t *testing.T) {
	t.Parallel()
	src, err := Find(t.TempDir())
	assert.ErrorIs(t, err, ErrNoMagusfile)
	assert.Nil(t, src)
}

// FuzzParse verifies that Parse never panics on arbitrary Buzz source content.
// The invariant: any bytes written as a .buzz file must produce either
// (targets, nil) or (nil, error) — never a panic.
func FuzzParse(f *testing.F) {
	f.Add([]byte("import \"magus\";\nexport fun build(ctx: magus\\Context, args: [str]) > void {}\n"))
	f.Add([]byte("import \"magus\";\nexport fun build(ctx: magus\\Context, args: [str]) > void {}\nexport fun test(ctx: magus\\Context, args: [str]) > void {}\n"))
	f.Add([]byte("var x: str = {"))
	f.Add([]byte(""))
	f.Add([]byte("\x00\x01\x02\x03"))
	f.Add([]byte("import \"magus\";\nexport fun noop(ctx: magus\\Context, args: [str]) > void {}\n"))

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
