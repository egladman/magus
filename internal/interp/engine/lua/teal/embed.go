// Package teal provides Teal (typed Lua) compiler support: embeds tl.lua, host type declarations, and compiled spell sources.
package teal

import _ "embed"

//go:embed vendor/tl.lua
var compiler string

// Version is the vendored teal-language/tl release.
const Version = "v0.24.8"

// LuaTarget is the Lua dialect emitted by tl.lua; must be compatible with gopher-lua (Lua 5.1 + goto).
const LuaTarget = "5.1"
