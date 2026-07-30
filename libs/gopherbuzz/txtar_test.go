package buzz

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// txtar is the multi-file fixture format for import tests: a comment, then one
// `-- path --` marker per file followed by its contents.
//
//	shared preamble, ignored
//	-- main.buzz --
//	import "lib/mod";
//	-- lib/mod.buzz --
//	export final answer = 42;
//
// The FORMAT is golang.org/x/tools' txtar; the reader below is gopherbuzz's own.
// rogpeppe/go-internal (which provides one) is a dependency of the magus module,
// not of gopherbuzz's, and gopherbuzz's module stays deliberately lean - a
// twenty-line parser is cheaper than a dependency that only tests use.
//
// This does NOT replace the `@expect` fixtures under testdata/: those are also
// round-tripped through the bytecode codec, which reads them in their own shape.
// txtar is for the case @expect cannot express - a program spread over several
// files, which is exactly where the interesting import behavior lives.

// txtarFile is one file in an archive.
type txtarFile struct {
	name string
	data string
}

// parseTxtar splits an archive into its files, dropping the leading comment.
// A marker line is `-- <name> --` with any surrounding space; anything before the
// first marker is the comment.
func parseTxtar(archive string) []txtarFile {
	var files []txtarFile
	var cur *txtarFile
	var body strings.Builder
	flush := func() {
		if cur != nil {
			cur.data = body.String()
			files = append(files, *cur)
			body.Reset()
		}
	}
	for _, line := range strings.Split(archive, "\n") {
		if name, ok := txtarMarker(line); ok {
			flush()
			cur = &txtarFile{name: name}
			continue
		}
		if cur != nil {
			body.WriteString(line)
			body.WriteString("\n")
		}
	}
	flush()
	return files
}

// txtarMarker reports whether line is a `-- name --` file marker, and the name.
func txtarMarker(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "--") || !strings.HasSuffix(s, "--") || len(s) < 4 {
		return "", false
	}
	name := strings.TrimSpace(s[2 : len(s)-2])
	if name == "" {
		return "", false
	}
	return name, true
}

// writeTxtar materializes an archive into a fresh temp dir and returns its path,
// so a test can declare a whole module tree where it reads it instead of in a run
// of os.MkdirAll/os.WriteFile calls.
func writeTxtar(t *testing.T, archive string) string {
	t.Helper()
	dir := t.TempDir()
	files := parseTxtar(archive)
	require.NotEmpty(t, files, "txtar archive declares no files")
	for _, f := range files {
		path := filepath.Join(dir, filepath.FromSlash(f.name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755), "mkdir for %s", f.name)
		require.NoError(t, os.WriteFile(path, []byte(f.data), 0o644), "write %s", f.name)
	}
	return dir
}
