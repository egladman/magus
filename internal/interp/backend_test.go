// Package interp_test wires the active backend and host bindings for all
// interp tests. These blank imports register the backend and host modules
// before any test runs.
package interp_test

import (
	_ "github.com/egladman/magus/internal/interp/bindings"
	_ "github.com/egladman/magus/internal/interp/engine/lua/gopherlua"
	_ "github.com/egladman/magus/internal/interp/engine/lua/luajit"
)
