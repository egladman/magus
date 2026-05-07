# Engines: how magus runs a magusfile

A **magusfile** can be written in either of two languages, and magus runs both
through the same internal seam. A `magusfile.tl` is [Teal](https://github.com/teal-language/tl)
(typed Lua) executed on a Lua VM; a `magusfile.bzz` is [Buzz](https://buzz-lang.dev/),
executed on the Buzz VM. They are interchangeable: the same `magus.*` API, the
same [spells](spells.md), [targets](targets.md), and [charms](charms.md) work on
both. This document explains the seam, its deliberate asymmetries, and how a new
language would plug in.

## The engine interface

Every backend implements one small interface, `engine.Engine`, which is a
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
script values without knowing the concrete VM. Backends register themselves at
`init()` time:

```go
// internal/interp/engine/buzz/buzz.go
func init() { engine.Register("buzz", engineImpl{}) }
```

and are found by name through `engine.Lookup(name)`. The registered backends in
a stock magus binary are `buzz`, `gopherlua` (pure-Go Lua), and `luajit` (cgo
Lua, when built with cgo). The Lua backends are interchangeable; preference order
lives in `internal/interp/registry.go` (`luajit` → `gopherlua`).

Backends are pulled into the binary by blank import, symmetrically, in
[`cmd/magus/packs_interp.go`](../cmd/magus/packs_interp.go):

```go
_ ".../internal/interp/engine/buzz"
_ ".../internal/interp/engine/lua/gopherlua"
_ ".../internal/interp/engine/lua/luajit"
```

## The engine-agnostic spell contract

A spell exports a fixed set of `mgs_`-prefixed functions (see
[spells.md](spells.md#authoring-a-custom-spell)). The list of optional functions
and the decoder keys they map to is **single-sourced** in
[`internal/spell/contract.go`](../internal/spell/contract.go) as
`OptionalContract`. Both resolvers iterate that one list:

- the Buzz resolver, `internal/spell/resolve.go`
- the Lua/Teal resolver, `resolveLua` in `internal/interp/bindings/spell.go`

so a Buzz spell and a Teal spell that declare the same `mgs_` functions decode to
the same `Spec` for every scalar and list contribution (`needs`, `provides`,
`claims`, `version_cmd`, `opaque`) and for record-shaped ops (`{cmd, args,
charms}`). The shared part of the contract is locked by `TestEngineSpecParity`
(`internal/interp/bindings/parity_test.go`), which resolves a spell declaring
every `mgs_` function through both paths and asserts the results are equal —
`claims` especially, the field the Lua path previously dropped.

Both engines support **function-ops** — ops whose handler does host work in-VM
rather than forking a command — so a remote cache backend (`get_artifact`/`put_artifact`)
can be authored in either language and wired with `magus.cache.remote`. The op
shapes differ by host language: a Buzz spell writes a handler that hands its
command to the injected `cb` callback (the built-ins' form, which the Buzz
resolver also extracts statically), while a Teal spell writes a **record** op
(`{cmd, args}`) for a fork and a **function-valued** op (`function(target, cb)
... end`) for a function-op. The two engines diverge on only one detail, by design:

- **Doc-comment capture.** Buzz captures a handler's doc comment at compile time
  (the parser binds the comment to the function node; `FunDoc` reads it back), and
  `magus doctor` enforces one on each function-handler target. Teal function-ops
  carry **no** captured doc and are **exempt** from that check. This is a deliberate
  non-goal, not a hard limitation: the comment isn't lost in the VM — Teal's lexer
  keeps comments in its AST and the `tl.gen` codegen simply drops them, and the
  Lua backends could expose a function's `linedefined` (via `debug.getinfo`) the way
  Buzz exposes `FunDoc`. Capturing it _reliably_ (associating each comment with the
  right op handler, including anonymous function literals) would mean extending the
  vendored Teal compiler — too much surface for too niche a payoff. Note Buzz's
  `Chunk.Doc` is in-memory only and not serialized to bytecode, so even Buzz
  captures docs only for freshly-compiled workspace `.bzz` spells, never the
  embedded built-ins; a Teal solution would match exactly that scope.

## Intentional Lua/Buzz asymmetries

The two engines are at parity for the `magus.*` host API, but each honors its own
**host language's** conventions rather than papering over them. These differences
are deliberate, not gaps:

- **`require` vs `import`.** Teal/Lua loads modules the standard Lua way, with
  dot-delimited names: `require("magus.spell.go")`, `require("magus.extra.os")`.
  Buzz is path-based, the standard Buzz way: `import "magus/spell/go"`,
  `import "magus/extra"`. Each follows its host language; neither is invented by
  magus.
- **`extra` is self-complete on both engines.** Every host module (including
  `json`, `crypto`, and the `fs`/`env` methods Buzz's stdlib also covers) is on
  both the Teal and Buzz surfaces. This is deliberate: it was tempting to make
  `extra` a strict _delta_ over Buzz's stdlib (omit the overlaps), but that split
  a single concept across two namespaces — in Buzz, `fs.exists` was native while
  `fs.join`/`glob` were `extra.fs`, so authors had to memorize which side each
  call lived on. Self-complete `extra` means one import covers a whole domain
  (`extra.fs.*`), and it does not shadow the stdlib (`extra.fs.exists` ≠ native
  `fs.exists`). The `extra` forms are also **sandbox-aware** where the bare
  stdlib is not — e.g. `extra.env.get`/`lookup` honor the env allowlist, while
  Buzz's `os.env` is raw. Methods that have a native Buzz equivalent are noted
  per-method in the [module reference](modules/index.md); either works. The
  cross-reference lives in `internal/std/buzz_overlap.go`.
- **Behavioral differences kept separate.** A few entries are _not_ treated as
  duplicates because the magus behavior the stdlib can't reproduce: magus's
  `os.exit` raises a lifecycle error (Buzz's hard-exits the process), magus's
  `os.sleep` is cancellable (Buzz's blocks), and magus's `crypto.*_file` hashes a
  file (Buzz's `hash` only takes a string). These stay on the magus surface for
  both engines.

The local-spell filename is the one place magus imposes a **cross-language**
convention: a workspace spell lives at `spells/<name>/spell.<ext>` (or flat
`spells/<name>.<ext>`), the same `spell.` basename for both `.tl` and `.bzz`.
Lua's usual `init.lua` is intentionally not used, so the layout reads the same
regardless of language.

## "Built-in spell" vs language "builtins"

A **built-in spell** is a spell whose bytecode is compiled from
`magus/spells/<name>/spell.bzz` and embedded in the magus binary (`go`,
`typescript`, `docker`, …; see [spells.md](spells.md#built-in)). This is a magus
concept and is unrelated to Buzz's language **builtins** (`spawn`, list/map
methods, etc.), which are part of the Buzz language itself. The docs always write
"built-in spell" when they mean the former.

## Adding a new language

The engine interface is the stable, clean part of the seam. Plugging in a third
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
   `internal/interp/bindings/buzz.go` and `lua.go` do today.

**Future direction — registry-driven discovery.** The intent is to derive
extensions, magusfile filenames, and dispatch from the engine registry itself, so
adding a language means registering a backend (with its extensions and binding
installer) and nothing else — no edits to `source.go`, `runtime.go`, or
switch statements. That refactor is deliberately out of scope for now; the
asymmetric, hard-coded spots above are the seam's known leaks, documented so they
are visible rather than surprising.

## See also

- [spells.md](spells.md): the `mgs_` spell contract and how spells compose.
- [targets.md](targets.md): the runnable unit and its CLI grammar.
- [modules/index.md](modules/index.md): the `magus.*` host module reference.
