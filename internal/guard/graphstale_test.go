package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCommandReadsGraph pins the guard-side trigger. It goes through the parser, so a
// wrapper resolves and a quoted word does not.
func TestCommandReadsGraph(t *testing.T) {
	for _, cmd := range []string{
		"magus refs Foo",
		"./magus query \"kind:target build\"",
		"magus explain target:.:ci",
		"mise exec -- magus path a b",
	} {
		assert.True(t, commandReadsGraph(cmd), cmd)
	}
	for _, cmd := range []string{
		"magus query output out1a2b3c",
		"magus run test .",
		"magus graph build",
		"grep -r refs .",
		"echo 'magus refs Foo'",
	} {
		assert.False(t, commandReadsGraph(cmd), cmd)
	}
}
