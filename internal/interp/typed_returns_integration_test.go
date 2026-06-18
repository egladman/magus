package interp_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecResultAnnotationChecksFields proves the typed-returns mechanism: a
// magusfile may annotate an exec result `> ExecResult` (from `import
// "magus/target"`), and because Buzz objects are structural the runtime map
// satisfies the type while the checker validates field access against the
// declared fields. A good field loads and runs; a typo'd field is a compile-time
// error, not a silent runtime null.
func TestExecResultAnnotationChecksFields(t *testing.T) {
	t.Run("good field type-checks and runs", func(t *testing.T) {
		dir := t.TempDir()
		writeMagusfile(t, dir, `
import "magus";
import "os";
import "fs";
import "magus/target";
export fun build(args: [str]) > void {
    final r: ExecResult = os.exec("echo", ["hi"]);
    fs.writeFile("ran", r.stdout);
}
`)
		require.NoError(t, runTarget(t, dir, "build"))
	})

	t.Run("typo'd field is a compile error", func(t *testing.T) {
		dir := t.TempDir()
		writeMagusfile(t, dir, `
import "magus";
import "os";
import "magus/target";
export fun build(args: [str]) > void {
    final r: ExecResult = os.exec("echo", ["hi"]);
    final x = r.stduot;
}
`)
		err := runTarget(t, dir, "build")
		require.Error(t, err, "r.stduot on an ExecResult must fail to compile")
		assert.Contains(t, strings.ToLower(err.Error()), "field",
			"error should name the missing field; got: %v", err)
	})
}
