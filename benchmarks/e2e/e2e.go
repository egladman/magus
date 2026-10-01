// Package e2e holds end-to-end orchestration tests for the public magus
// API. It lives in its own package because exercising Run/RunCI requires
// blank-importing the host bindings, whose init() registers the built-in spells
// process-wide — a side effect that would collide with the spell fixtures in the
// magus package's own tests.
package e2e

import (
	// Link the host bindings so magusfile.buzz targets execute. Linking them is
	// this package's one production effect, and the reason it is a package.
	_ "github.com/egladman/magus/internal/interp/bindings"
)
