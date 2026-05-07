package interp

import (
	"context"

	teal "github.com/egladman/magus/internal/interp/engine/lua/teal"
)

// CompileTealFile compiles Teal source at srcPath to Lua. preamble is prepended before type-checking.
// For code-generation tools; not for runtime use.
func CompileTealFile(ctx context.Context, srcPath, preamble string) ([]byte, error) {
	return teal.CompileFile(ctx, srcPath, preamble)
}

// TypeDecls returns the concatenated .d.tl declarations for use as CompileTealFile's preamble.
func TypeDecls() (string, error) {
	return teal.TypeDecls()
}
