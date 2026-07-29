package emit

import (
	"os"
	"path/filepath"
	"testing"
	"text/template"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFileLeavesUnchangedFileAlone pins the mtime behavior the cache
// depends on. Rewriting identical bytes marks every downstream target dirty, so
// "wrote the same thing" and "did not write" are different outcomes here.
func TestFileLeavesUnchangedFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	require.NoError(t, File(path, []byte("hello")))

	before, err := os.Stat(path)
	require.NoError(t, err)

	// Backdate so an unwanted rewrite is detectable without sleeping.
	old := before.ModTime().Add(-time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))

	require.NoError(t, File(path, []byte("hello")))
	after, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, after.ModTime().Equal(old), "identical content must not rewrite the file")

	require.NoError(t, File(path, []byte("goodbye")))
	changed, err := os.Stat(path)
	require.NoError(t, err)
	assert.False(t, changed.ModTime().Equal(old), "changed content must rewrite the file")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "goodbye", string(got))
}

func TestFileCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")
	require.NoError(t, File(path, []byte("x")))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, FileMode, info.Mode().Perm())
}

// TestGoRejectsUnparseable is the guard's point: a template emitting
// broken Go must fail naming the file, not at the next build inside generated output.
func TestGoRejectsUnparseable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.go")
	err := Go(path, []byte("package p\nfunc ("))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gofmt")
	assert.NoFileExists(t, path, "a file that failed to format must not be written")
}

func TestGoFormats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ok.go")
	require.NoError(t, Go(path, []byte("package p\nvar  x   =  1")))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "package p\n\nvar x = 1\n", string(got))
}

func TestGoTemplate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rendered.go")
	tmpl := template.Must(template.New("t").Parse("package p\nvar Name = {{printf \"%q\" .}}"))
	require.NoError(t, GoTemplate(path, tmpl, "magus"))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "package p\n\nvar Name = \"magus\"\n", string(got))
}

func TestRegion(t *testing.T) {
	m := CommentMarker("#", "subcommands")
	src := "before\n    " + m.Begin + "\nstale\nlines\n    " + m.End + "\nafter"

	got, err := Region(src, m, "fresh")
	require.NoError(t, err)
	assert.Equal(t, "before\n    "+m.Begin+"\nfresh\n    "+m.End+"\nafter", got,
		"both marker lines and their indentation must survive")

	// Idempotent: replacing with what is already there is a no-op.
	again, err := Region(got, m, "fresh")
	require.NoError(t, err)
	assert.Equal(t, got, again)
}

// TestRegionRefusesAmbiguity covers the cases where appending or guessing
// would corrupt a hand-maintained file.
func TestRegionRefusesAmbiguity(t *testing.T) {
	m := CommentMarker("#", "list")
	for name, src := range map[string]string{
		"no markers at all": "just some text",
		"begin only":        "a\n" + m.Begin + "\nb",
		"end only":          "a\n" + m.End + "\nb",
		"reversed":          m.End + "\nbody\n" + m.Begin,
		"duplicate begin":   m.Begin + "\nx\n" + m.Begin + "\ny\n" + m.End,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Region(src, m, "body")
			assert.Error(t, err, "must refuse rather than guess where the region belongs")
		})
	}
}
