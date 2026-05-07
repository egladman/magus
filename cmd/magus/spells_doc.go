package main

// Spell registration: spells are authored in Teal, compiled to Lua under
// magus/internal/interp/engine/lua/teal/spell/<name>/gen/<name>.lua, and loaded
// into the Lua VM at runtime via package.preload (key: "magus.spell.<name>").
// No blank-import or project.RegisterSpell call is required; the bindings
// layer discovers all spells from the embedded filesystem at startup.
//
// To attach a spell to a project from a magusfile.tl:
//
//	local go = require("magus.spell.go")
//	magus.project.register(".", { spells = { go } })
//
// Or from a Go magusfile using the magus package:
//
//	magus.RegisterProject(".", magus.WithSpell("go"))
//
// To call a spell's targets directly from a Teal target:
//
//	local go = require("magus.spell.go")
//	go.build({cwd = "."})
//
// Available spells live under magus/internal/interp/engine/lua/teal/spell/<name>/.
// Each spell is a <name>.tl source file; regenerate compiled Lua with:
//
//	magus run spells-generate
//
// Runtime utility modules are imported per file with require:
//
//	local os = require("magus.extra.os")   -- os.exec (direct) / os.exec_sh (shell)
//	local fs = require("magus.extra.fs")   -- filesystem helpers
//	local vcs = require("magus.extra.vcs") -- VCS introspection
