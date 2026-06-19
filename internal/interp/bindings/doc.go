// Package bindings registers the Go-backed modules (magus, std: os, platform,
// fs, vcs, env, crypto, json, log, http, archive) available to every magusfile script.
// Blank-import this package so its init() fires before any magusfile runs.
//
// Layout:
//   - buzz.go        assembles the magus.* namespace and wires it onto a session;
//     each sub-namespace it calls lives in its own file.
//   - modules.go     the host module surface (os/fs/http/…) layered over Buzz's stdlib.
//   - imports.go     resolves `import "project/…"` and `import "spells/…"`.
//   - project_ns.go  magus.project and its option decoding.
//   - target_ns.go   magus.target/needs/cache and same/cross-project dispatch.
//   - spell_object.go  the Buzz handle an imported spell exposes (per-target methods).
//   - pry.go         magus.pry: the REPL/stepping breakpoint.
//   - marshal.go     the Buzz↔Go value boundary: argStr/argStrSlice/argStrMap
//     pull typed Go values out of []buzz.Value arguments; strSliceToBuzzList and
//     buzzValToStringSlice go the other way (the spell-shaped Go→Buzz marshalers
//     live next to their handles in spell_object.go).
//   - spell.go, spell_buzz.go, command.go  spell loading, op dispatch, and command execution.
//   - remote_cache.go  adapts a spell to the cache's remote-backend contract.
//
// Every binding is a buzz.DirectValue closure of the shape
// func(ctx, []buzz.Value) (buzz.Value, error); the marshal.go helpers keep
// the argument-decoding at those call sites uniform rather than hand-rolled.
package bindings
