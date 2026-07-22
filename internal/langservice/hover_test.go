package langservice

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHover_ModuleMethod(t *testing.T) {
	src := "import \"fs\";\nfs.glob(\"*\");"
	// Point at the "glob" identifier.
	off := strings.Index(src, "glob") + 1
	h := HoverAt(src, off)
	require.NotNil(t, h, "expected hover on fs.glob")
	assert.Contains(t, h.Title, "glob", "title should name the method: %q", h.Title)
	assert.NotEmpty(t, h.Doc, "method hover should carry docs")
}

func TestHover_ModuleName(t *testing.T) {
	src := "import \"fs\";"
	off := strings.Index(src, "fs") + 1
	h := HoverAt(src, off)
	require.NotNil(t, h)
	assert.Contains(t, h.Title, "fs")
	assert.NotEmpty(t, h.Doc)
}

func TestHover_LocalFunction(t *testing.T) {
	src := "export fun build(ctx: magus\\Context, args: [str]) > void {}\nbuild();"
	off := strings.LastIndex(src, "build") + 1
	h := HoverAt(src, off)
	require.NotNil(t, h)
	assert.Contains(t, h.Title, "build")
	assert.Contains(t, h.Title, "args", "local function hover should show its signature")
}

func TestHover_Nothing(t *testing.T) {
	assert.Nil(t, HoverAt("   +  ", 3), "no symbol under cursor")
	assert.Nil(t, HoverAt("xyzzy", 2), "unknown identifier has no hover")
}

func TestHover_OffsetClamped(t *testing.T) {
	assert.NotPanics(t, func() {
		HoverAt("fs.glob", 999)
		HoverAt("fs.glob", -3)
	})
}
