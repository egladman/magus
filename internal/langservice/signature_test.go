package langservice

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignatureAt_ModuleMethod(t *testing.T) {
	src := "import \"fs\";\nfs\\glob(\"*\")"
	// Cursor inside the argument list.
	off := strings.Index(src, "(") + 1
	sig := SignatureAt(src, off)
	require.NotNil(t, sig, "expected signature help inside fs\\glob(...)")
	assert.Contains(t, sig.Label, "glob")
	assert.NotEmpty(t, sig.Doc)
}

func TestTypedLoaderEditorMetadata(t *testing.T) {
	const member = "progress\\newRotation"
	const signature = member + "(name: RotationName) > int !> any"
	item, ok := findLabel(CompleteAt(member, len(member)), "newRotation")
	require.True(t, ok)
	assert.Equal(t, signature, item.Detail)
	require.NotEmpty(t, item.Doc)
	hover := HoverAt(member, len(member))
	require.NotNil(t, hover)
	assert.Equal(t, signature, hover.Title)
	assert.Equal(t, item.Doc, hover.Doc)
	help := SignatureAt(member+"(", len(member)+1)
	require.NotNil(t, help)
	assert.Equal(t, signature, help.Label)
	assert.Equal(t, item.Doc, help.Doc)
}

func TestSignatureAtPreservesDeclaredDefaultsAndVariadics(t *testing.T) {
	for member, want := range map[string]string{
		"magus\\cmd":   "magus\\cmd(sub: str, args: [str], opts: CommandOptions? = null) > ExecResult !> any",
		"fmt\\sprintf": "fmt\\sprintf(format, args...) -> string",
	} {
		sig := SignatureAt(member+"(", len(member)+1)
		require.NotNil(t, sig)
		assert.Equal(t, want, sig.Label)
	}
}

func TestSignatureAt_LocalFunction(t *testing.T) {
	src := "import \"magus\";\nexport fun build(ctx: magus\\Context, args: [str]) > void {}\nbuild("
	sig := SignatureAt(src, len(src))
	require.NotNil(t, sig)
	assert.Contains(t, sig.Label, "build")
	assert.Contains(t, sig.Label, "args")
}

func TestSignatureAt_NotInCall(t *testing.T) {
	assert.Nil(t, SignatureAt("final x = 1;", 8), "no enclosing call")
	assert.Nil(t, SignatureAt("fs\\glob(\"*\")", len("fs\\glob(\"*\")")), "cursor past the closed call")
	assert.Nil(t, SignatureAt("import \"fs\";\nfs.glob(", len("import \"fs\";\nfs.glob(")), "a dot reaches a value's member, not a module's")
	assert.Nil(t, SignatureAt("final xs = [1];\nxs.len(", len("final xs = [1];\nxs.len(")), "a value's len is not the builtin")
}

func TestSignatureAt_Clamped(t *testing.T) {
	assert.NotPanics(t, func() {
		SignatureAt("fs\\glob(", 999)
		SignatureAt("fs\\glob(", -4)
	})
}
