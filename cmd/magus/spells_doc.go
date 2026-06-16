package main

// Spell registration: built-in spells are authored in Buzz under
// magus/spells/<name>/spell.buzz, compiled to bytecode and embedded at build
// time (see cmd/magus-spells-gen), and exposed to magusfiles as the import
// "magus/spell/<name>". No blank-import or project.RegisterSpell call is
// required; the bindings layer discovers all spells from the embedded
// filesystem at startup.
//
// To attach a spell to a project from a magusfile.buzz:
//
//	import "magus";
//	import "magus/spell/go";
//	magus.project.register(fun(p, cb) > bool { cb({ "spells": [go] }); return true; });
//
// Or from a Go magusfile using the magus package:
//
//	magus.RegisterProject(".", magus.WithSpell("go"))
//
// To call a spell's targets directly from a Buzz target:
//
//	import "magus/spell/go";
//	go.build({ "cwd": "." });
//
// Built-in spells live under magus/spells/<name>/; each is a spell.buzz source
// file. Regenerate the embedded bytecode with:
//
//	magus run spells-generate
//
// Host utility modules are imported per file off the magus/extra aggregate:
//
//	import "magus/extra";  // extra.os.exec (direct) / extra.os.exec_sh (shell),
//	                       // extra.fs (filesystem), extra.vcs (VCS introspection)
