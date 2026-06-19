// Package interp compiles and runs magusfile sources via the Buzz scripting backend.
// Owns the host-binding seam, REPL, and compiled-source cache.
package interp

import (
	"github.com/egladman/magus/internal/interp/engine"
)

// Available reports whether the interp layer can run magusfiles: the Buzz engine
// is registered and Buzz host bindings are installed. Both are always present in a
// real magus binary (blank-imported from cmd/magus).
func Available() bool {
	return buzzHostBindingsFn != nil && engine.Lookup("buzz") != nil
}
