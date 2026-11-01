package result

import (
	_ "embed"
)

// ModulePath is the import spelling. It stays outside magus/: importing
// there forces the full workspace load, and a hook may not.
const ModulePath = "result"

// Source is the result module: Diagnostic, Result, ok, fail and caught. It
// imports nothing, so any session can offer it.
//
//go:embed result.buzz
var Source string
