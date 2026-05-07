// Package std embeds the Teal type-declaration files for the magus DSL and std modules.
package std

import "embed"

//go:embed *.d.tl tlconfig.lua
var FS embed.FS
