package dry

import "context"

// Diagnostics is [diagnostics] for the playground's editor squiggles, the one
// caller, which only the wasm build compiles.
func Diagnostics(ctx context.Context, src string) []Diag {
	return diagnostics(ctx, src)
}
