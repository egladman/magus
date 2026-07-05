package langservice

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignatureAt_ModuleMethod(t *testing.T) {
	src := "import \"fs\";\nfs.glob(\"*\")"
	// Cursor inside the argument list.
	off := strings.Index(src, "(") + 1
	sig := SignatureAt(src, off)
	require.NotNil(t, sig, "expected signature help inside fs.glob(...)")
	assert.Contains(t, sig.Label, "glob")
	assert.NotEmpty(t, sig.Doc)
}

func TestSignatureAt_LocalFunction(t *testing.T) {
	src := "export fun build(args: [str]) > void {}\nbuild("
	sig := SignatureAt(src, len(src))
	require.NotNil(t, sig)
	assert.Contains(t, sig.Label, "build")
	assert.Contains(t, sig.Label, "args")
}

func TestSignatureAt_NotInCall(t *testing.T) {
	assert.Nil(t, SignatureAt("final x = 1;", 8), "no enclosing call")
	assert.Nil(t, SignatureAt("fs.glob(\"*\")", len("fs.glob(\"*\")")), "cursor past the closed call")
}

func TestSignatureAt_Clamped(t *testing.T) {
	assert.NotPanics(t, func() {
		SignatureAt("fs.glob(", 999)
		SignatureAt("fs.glob(", -4)
	})
}
