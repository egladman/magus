package url

import "github.com/egladman/magus/std"

// Modules returns this directory's contribution to the encoding module set.
// See std/encoding/register.go: it aggregates every leaf's Modules() into one
// slice, which is how a module here reaches std.All()'s callers without std
// importing this package to collect it.
func Modules() []std.Module { return []std.Module{Module} }
