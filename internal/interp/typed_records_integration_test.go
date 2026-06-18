package interp_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordAnnotationsCheckFields proves every host-record mirror shipped in
// magus/target gives compile-checked field access: annotating a host result with
// the object type makes a valid field compile and a typo'd field a compile error.
// The annotated access lives in an unexecuted probe fn, so the file type-checks
// without the test performing any network/git/exec — only build (a no-op) runs.
func TestRecordAnnotationsCheckFields(t *testing.T) {
	cases := []struct {
		typ, expr, good, bad string
		imports              string
	}{
		{"ExecResult", `os.exec("echo", ["hi"])`, "stdout", "stduot", `import "os";`},
		{"Commit", `vcs.commit()`, "author", "auther", `import "vcs";`},
		{"FileInfo", `fs.stat(".")`, "size", "sizes", `import "fs";`},
		{"HttpResponse", `http.get("http://x")`, "status", "staus", `import "http";`},
		{"SemverVersion", `semver.parse("1.2.3")`, "major", "majr", `import "semver";`},
		{"URL", `encoding.parseUrl("http://x")`, "scheme", "sceme", `import "encoding";`},
	}

	mkfile := func(c struct{ typ, expr, good, bad, imports string }, field string) string {
		return fmt.Sprintf(`
import "magus";
import "fs";
import "magus/target";
%s
fun probe() > void {
    final r: %s = %s;
    final _ = r.%s;
}
export fun build(args: [str]) > void {
    fs.writeFile("ran", "ok");
}
`, c.imports, c.typ, c.expr, field)
	}

	for _, c := range cases {
		c := c
		t.Run(c.typ, func(t *testing.T) {
			good := t.TempDir()
			writeMagusfile(t, good, mkfile(struct{ typ, expr, good, bad, imports string }(c), c.good))
			require.NoError(t, runTarget(t, good, "build"),
				"%s: valid field %q should compile and the no-op target should run", c.typ, c.good)

			bad := t.TempDir()
			writeMagusfile(t, bad, mkfile(struct{ typ, expr, good, bad, imports string }(c), c.bad))
			err := runTarget(t, bad, "build")
			require.Error(t, err, "%s: typo'd field %q must fail to compile", c.typ, c.bad)
			assert.Contains(t, strings.ToLower(err.Error()), "field",
				"%s: error should name the missing field; got: %v", c.typ, err)
		})
	}
}
