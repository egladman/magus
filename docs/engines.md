# Engines: how magus runs a magusfile

A **magusfile** is written in [Buzz](https://buzz-lang.dev/) and runs on the
embedded Buzz VM through a small internal seam. A `magusfile.buzz` exposes the
`magus.*` API and composes [spells](spells.md), [targets](targets.md), and
[charms](charms.md). This document explains the seam and how a new language
would plug in.

## The engine interface

The backend implements one small interface, `engine.Engine`, which is a
factory for `engine.Session`:

```go
// internal/interp/engine/engine.go
type Engine interface {
    ID() string
    NewSession(ctx context.Context) (Session, error)
}

type Session interface {
    Close() error
    SetGlobal(name string, v Value)
    GetGlobal(name string) Value
    NewTable() Table
    LoadString(code string) (Value, error)
    DoString(code string) error
    Call(p CallParams, args ...Value) error
}
```

`Value` and `Table` are engine-neutral handles, so host code reads and writes
script values without knowing the concrete VM. The backend registers itself at
`init()` time:

```go
// internal/interp/engine/buzz/buzz.go
func init() { engine.Register("buzz", engineImpl{}) }
```

and is found by name through `engine.Lookup(name)`. The registered backend in a
stock magus binary is `buzz`.

The backend is pulled into the binary by blank import in
[`cmd/magus/packs_interp.go`](../cmd/magus/packs_interp.go):

```go
_ ".../internal/interp/engine/buzz"
```

## The spell contract

A spell exports a fixed set of `mgs_`-prefixed functions (see
[spells.md](spells.md#authoring-a-custom-spell)). The list of optional functions
and the decoder keys they map to is **single-sourced** in
[`internal/spell/contract.go`](../internal/spell/contract.go) as
`OptionalContract`, and the Buzz resolver (`internal/spell/resolve.go`) iterates
that one list. A spell's `mgs_` functions decode to a `Spec` for every scalar and
list contribution (`needs`, `provides`, `claims`, `version_cmd`, `opaque`) and
for record-shaped ops (`{cmd, args, charms}`).

Spells support **function-ops** — ops whose handler does host work in-VM rather
than forking a command — so a remote cache backend (`get_artifact`/`put_artifact`)
can be authored and wired with `magus.cache.remote`. A handler hands its command
to the injected `cb` callback (the form the resolver also extracts statically).

**Doc-comment capture.** Buzz captures a handler's doc comment at compile time
(the parser binds the comment to the function node; `FunDoc` reads it back), and
`magus doctor` enforces one on each function-handler target. Note Buzz's
`Chunk.Doc` is in-memory only and not serialized to bytecode, so Buzz captures
docs only for freshly-compiled workspace `.buzz` spells, never the embedded
built-ins.

## Host modules are a superset of Buzz's stdlib

magus layers its host methods onto Buzz's own stdlib modules under the **same bare
names**: `import "os"` carries both Buzz's `os.*` (sleep, env, execute) and magus's
additions (`os.exec`, `os.which`, …); `import "fs"` carries Buzz's `fs` plus
`fs.glob`/`readFile`; and magus adds whole modules Buzz lacks (`vcs`, `archive`,
`http`, `charm`, …). One import per domain covers the union — there's no separate
`extra` namespace to remember which side a call lives on.

Where a method overlaps a Buzz stdlib call, the magus form is **sandbox-aware**
while the bare stdlib is not — e.g. `env.get`/`lookup` honor the env allowlist,
whereas Buzz's `os.env` is raw. Those overlaps are noted per-method in the
[module reference](modules/index.md) (either works); the cross-reference lives in
`hostbuzz/overlap.go`.

A few entries are _not_ treated as duplicates because the magus behavior the
stdlib can't reproduce: magus's `os.exit` raises a lifecycle error (Buzz's
hard-exits the process), magus's `os.sleep` is cancellable (Buzz's blocks), and
magus's `crypto.*_file` hashes a file (Buzz's `hash` only takes a string). These
stay on the magus surface.

A workspace spell lives at `spells/<name>/spell.buzz` (or flat
`spells/<name>.buzz`).

## "Built-in spell" vs language "builtins"

A **built-in spell** is a spell whose bytecode is compiled from
`magus/spells/<name>/spell.buzz` and embedded in the magus binary (`go`,
`typescript`, `docker`, …; see [spells.md](spells.md#built-in)). This is a magus
concept and is unrelated to Buzz's language **builtins** (`spawn`, list/map
methods, etc.), which are part of the Buzz language itself. The docs always write
"built-in spell" when they mean the former.

## Adding a new language

The engine interface is the stable, clean part of the seam. Plugging in a second
language today, however, also touches a handful of **hard-coded dispatch spots**
above the interface — this is honest current state, not the end state:

1. Implement `engine.Engine`/`Session` for the VM and `engine.Register` it (the
   clean part).
2. Map the file extension to the engine in `engineForExt`
   (`internal/interp/source.go`) and add the glob to `scriptExts` /
   the `magusfile.<ext>` lists.
3. Branch the runtime where it special-cases an engine by name
   (`src.Engine == "buzz"` in `internal/interp/runtime.go`).
4. Provide the per-engine host bindings (the `magus.*` surface), as
   `internal/interp/bindings/buzz.go` does today.

**Future direction — registry-driven discovery.** The intent is to derive
extensions, magusfile filenames, and dispatch from the engine registry itself, so
adding a language means registering a backend (with its extensions and binding
installer) and nothing else — no edits to `source.go`, `runtime.go`, or
switch statements. That refactor is deliberately out of scope for now; the
hard-coded spots above are the seam's known leaks, documented so they
are visible rather than surprising.

## See also

- [spells.md](spells.md): the `mgs_` spell contract and how spells compose.
- [targets.md](targets.md): the runnable unit and its CLI grammar.
- [modules/index.md](modules/index.md): the `magus.*` host module reference.
