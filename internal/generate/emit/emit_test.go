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

// TestGoRejectsUnparsable is the guard's point: a template emitting
// broken Go must fail naming the file, not at the next build inside generated output.
func TestGoRejectsUnparsable(t *testing.T) {
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

// TestFileSurfacesAnUnreadablePath covers the read error that is neither nil nor
// NotExist. The distinction matters: a missing file is the normal first-generation
// case and must fall through to the write, while anything else means the path is
// not a file this generator may own - and silently proceeding to WriteFile there
// would report success for output that never landed.
func TestFileSurfacesAnUnreadablePath(t *testing.T) {
	dir := t.TempDir()
	// A directory where a file is expected: ReadFile fails with neither nil nor
	// NotExist, which is exactly the branch under test.
	occupied := filepath.Join(dir, "occupied")
	require.NoError(t, os.Mkdir(occupied, 0o755))

	err := File(occupied, []byte("data"))
	require.Error(t, err, "a directory at the target path must not be reported as a successful write")
	assert.NotErrorIs(t, err, os.ErrNotExist, "NotExist is the create case, not this one")
}

// TestGoTemplateNamesTheFailingKey covers the template-execution branch. missingkey
// =error is what turned a misspelled field from a silent "<no value>" in shipped
// output into a failure, so the error must name the lookup that failed - otherwise
// the report is "template failed" over a template with fifty fields.
func TestGoTemplateNamesTheFailingKey(t *testing.T) {
	tmpl := template.Must(template.New("x").Option("missingkey=error").
		Parse("package p\n\nconst V = \"{{.Nope}}\"\n"))

	path := filepath.Join(t.TempDir(), "out.go")
	err := GoTemplate(path, tmpl, map[string]string{"Yep": "v"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Nope", "the error must name the missing key")
	assert.NoFileExists(t, path, "a failed template must not leave a partial file")
}

// TestGoTemplateSurfacesAWriteFailure covers the branch after a SUCCESSFUL format:
// the template rendered, gofmt accepted it, and only the write failed. Reported as
// a template error it would send the reader to the template that was fine.
func TestGoTemplateSurfacesAWriteFailure(t *testing.T) {
	tmpl := template.Must(template.New("x").Parse("package p\n"))
	// A path under a non-directory: formatting succeeds, writing cannot.
	file := filepath.Join(t.TempDir(), "notadir")
	require.NoError(t, os.WriteFile(file, []byte("x"), FileMode))

	err := GoTemplate(filepath.Join(file, "out.go"), tmpl, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write", "a write failure must not read as a template failure")
}

// TestRegionIsIdempotent is the property the marker design rests on, and nothing
// proved it: re-running a generator over its own output must be a no-op. If the
// second pass differed, every generate run would dirty the file and the cache
// would rebuild the world on a target that changed nothing.
func TestRegionIsIdempotent(t *testing.T) {
	m := CommentMarker("#", "subcommands")
	src := "before\n" + m.Begin + "\nstale\n" + m.End + "\nafter\n"

	once, err := Region(src, m, "run\ntest")
	require.NoError(t, err)
	twice, err := Region(once, m, "run\ntest")
	require.NoError(t, err)
	assert.Equal(t, once, twice, "a generator re-run over its own output must not change it")

	// And it converges from any prior body, not only from its own: replacing a
	// longer region with a shorter one must not leave a tail behind.
	shorter, err := Region(once, m, "run")
	require.NoError(t, err)
	assert.NotContains(t, shorter, "test", "the previous body must be gone, not appended to")
}

// TestRegionTreatsAMarkerInAStringLiteralAsAMarker pins the known limitation
// rather than pretending it does not exist. Region is line-based and matches by
// substring, so a marker spelled inside a string literal in the target file IS
// found and the "region" between it and the real end marker is replaced.
//
// This is left as-is deliberately. Every consumer is a generator writing a region
// into an artifact it owns (four shell completion scripts), the markers carry a
// magus-utils: prefix no unrelated line would spell by accident, and teaching
// Region to lex Go and four shell dialects to avoid it would be far more machinery
// than the hazard justifies. The test exists so the behavior is a documented
// choice, and so anyone who later needs marker-in-string to be safe finds this
// first instead of discovering it in a corrupted artifact.
func TestRegionTreatsAMarkerInAStringLiteralAsAMarker(t *testing.T) {
	m := CommentMarker("//", "list")
	src := "package p\n\nconst doc = \"" + m.Begin + "\"\n" + m.End + "\ntail\n"

	out, err := Region(src, m, "BODY")
	require.NoError(t, err, "the quoted marker is matched, not skipped")
	assert.Contains(t, out, "BODY")
	assert.Contains(t, out, "const doc", "the marker line itself is preserved, quotes and all")
}

// TestRegionRefusesADuplicateEndMarker completes the ambiguity guard. A duplicated
// BEGIN was already covered; a duplicated END is the more dangerous half, because
// the first-wins reading would silently truncate everything between the two ends -
// deleting hand-written content the generator does not own.
func TestRegionRefusesADuplicateEndMarker(t *testing.T) {
	m := CommentMarker("#", "list")
	src := m.Begin + "\nbody\n" + m.End + "\nkeep me\n" + m.End + "\n"

	_, err := Region(src, m, "new")
	require.Error(t, err, "two end markers is ambiguous, and guessing would delete 'keep me'")
	assert.Contains(t, err.Error(), "more than once")
}
