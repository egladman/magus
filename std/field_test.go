package std

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNoModuleDeclaresFields keeps the host surface to members Buzz can declare.
//
// A Method becomes `export extern fun name() > str` in the generated declarations,
// so the checker knows its type and a caller's mistake is a compile error. A Field
// becomes a plain value on the module map and gets NO declaration at all: Buzz's
// parser accepts `extern` only before `fun` (parser.go), and upstream Buzz declares
// its whole native stdlib the same way, so there is no syntax for an extern value to
// generate. The checker therefore cannot type a Field, and nothing about it is
// checkable.
//
// That is not theoretical. vcs.name and vcs.base were Fields; four of the module's
// own doc-strings described `vcs.name()` with call parens, the docs site, the buzz
// reference and the editor hovers all render from those doc-strings, and a magusfile
// written against them compiled clean and failed at RUNTIME with "str is not
// callable" - inside a branch that only executes in CI. Both are Methods now, and
// `vcs.name()` is what the surface both documents and accepts.
//
// The Field machinery is still wired (magus-docs, langservice-manifest, and the
// ModuleFieldEntry boundary type all render it) because removing it would change a
// Buzz-visible introspection shape for no functional gain. This gate is what keeps it
// unused: a constant that cannot be type-checked is not worth the parens it saves.
func TestNoModuleDeclaresFields(t *testing.T) {
	for _, m := range All() {
		assert.Emptyf(t, m.Fields,
			"module %q declares Fields, which generate no extern declaration and so cannot be\n"+
				"type-checked - a caller writing %s\\%s() compiles clean and fails at runtime.\n"+
				"Declare it as a Method returning the value instead.",
			m.Name, m.Name, fieldNames(m))
	}
}

func fieldNames(m Module) string {
	if len(m.Fields) == 0 {
		return "<field>"
	}
	return m.Fields[0].Name
}
