package main

// Spell registration: built-in spells are authored in Buzz under
// spells/<name>/spell.buzz, embedded as source (spells.Shipped) and compiled at
// startup, and exposed to magusfiles as the import "magus/spell/<name>". No
// blank-import or project.RegisterSpell call is required; the bindings layer
// discovers all spells from the embedded filesystem at startup.
//
// To attach a spell to a project from a magusfile.buzz:
//
//	import "magus";
//	import "magus/spell/go";
//	magus.project({ "spells": [go] });
//
// Or from a Go magusfile using the magus package:
//
//	reg := magus.NewWorkspaceRegistry()
//	reg.RegisterProject(".", magus.WithSpell("go"))
//
// To call a spell's targets directly from a Buzz target:
//
//	import "magus/spell/go";
//	go.build({ "cwd": "." });
//
// Built-in spells live under spells/<name>/; each is a spell.buzz source
// file, and a rebuild of the binary is all an edit needs. Copy one out to fork
// it with:
//
//	magus spell pull magus/spell/<name> <dir>
//
// Host utility modules are imported directly, one bare import per module:
//
//	import "os";   // proc.exec (direct) / os.execSh (shell)
//	import "fs";   // filesystem
//	import "vcs";  // VCS introspection
