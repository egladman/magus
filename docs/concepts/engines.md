---
title: Engines
description: Why magus embeds Buzz, what that embedding buys over an external SDK, and the small engine.Engine seam a new scripting language would plug into.
tags: [engines, buzz, vm, interpreter, runtime, magusfile, plugin, session]
order: 12
---

# Engines

A **magusfile** is written in [Buzz](https://buzz-lang.dev/) and runs on the
embedded Buzz VM through a small internal seam. A `magusfile.buzz` exposes the
`magus.*` API and composes [spells](spells.md), [targets](targets.md), and
[charms](charms.md). This page covers the seam and how a new language
would plug in.

## Why Buzz

I will not pretend this was an ideological choice. magus is a solo project, and
I had never written an interpreter before this one. The requirement I actually
had was narrow: **statically typed** and **relatively explicit**, so a
magusfile reads like a program rather than a config format someone has to
reverse-engineer. Buzz met that bar and was small enough for one person to
implement end to end (`libs/gopherbuzz` is my own bytecode VM, not a wrapped
third-party runtime; see [`libs/gopherbuzz`](https://github.com/egladman/magus/tree/main/libs/gopherbuzz)). Nothing here is a claim that Buzz is the best language
for the job - only that it satisfies the two properties I actually wanted, and
those two properties are what the rest of this section argues for.

**What embedding buys that an external SDK cannot.** If magus shelled out to a
separate interpreter (or asked you to write your build in a general-purpose
SDK and call a library), the tool would only ever see what that program does
when you run it. Embedding the VM in the same binary lets magus treat the
magusfile as data it can inspect, not just code it executes:

- The same binary that runs a target also **statically extracts every
  cache-footprint glob before any run** - the walk over the AST that
  `ctx.readsFiles`/`ctx.writesFiles`/a spell's `needs`/`provides` feed is
  read-only and happens at load, well before a target's body is ever called
  (see [cache.md](cache.md#files-a-target-edits-rather-than-produces)).
- A **non-literal glob is a load-time error**, not a silent gap: if the
  argument isn't a string literal the static walk can see, magus refuses to
  load rather than guess (see the same section).
- `magus describe`/`magus graph` derive the **target DAG statically**,
  including **both arms of a runtime charm branch**
  (`if ctx.has_charm("cd") { ctx.needs(a) } else { ctx.needs(b) }` shows both
  edges) - a static graph question and a dry run are allowed to disagree,
  because they answer different questions (see
  [dependencies.md](dependencies.md#both-arms-rule-the-static-graph-and-a-dry-run-can-disagree)).

None of that is available to a tool that treats the build description as an
opaque program handed to some other runtime. It requires owning the parser and
the checker.

**The vendoring stance.** magus embeds its own pure-Go implementation of Buzz,
`libs/gopherbuzz`, deliberately tracking upstream `buzz-lang/buzz` rather than
shelling out to the reference Zig implementation or forking away from it. That
is a compatibility decision, not a survival one: if upstream Buzz development
stopped tomorrow, magus would keep running on whatever subset gopherbuzz
already implements - the risk is that gopherbuzz's own conformance work (see
[`libs/gopherbuzz`](https://github.com/egladman/magus/tree/main/libs/gopherbuzz)) would have no
upstream to converge toward, not that magusfiles would stop executing.

**The honest cost.** Nobody arrives at magus already knowing Buzz. That is a
real onboarding tax, and this section is not trying to argue it away. It is
also worth being precise about which Buzz a magusfile is written in: `magus`
runs it in a **relaxed dialect** - top-level statements outside a function,
optional argument labels - that upstream's own strict parser rejects. Use
`magus buzz --embedded` to parse and run a file (or a REPL) under those same
relaxed rules instead of upstream-strict ones (`magus buzz --help` for the
full flag list).

**What evaluating a magusfile actually guarantees.** Magusfile evaluation is
Turing-complete Buzz: it may perform workspace-confined I/O at load (reading a
file to decide what to declare, for instance), and magus does not guarantee it
terminates. What magus does bound is narrower and more useful: the I/O a load
can perform is confined to the sandbox exactly as a target's own execution is
(see [sandbox.md](sandbox.md)), and re-deriving the graph from an unchanged
tree is deterministic - the same magusfile loaded twice produces the same
targets, the same declared footprints, and the same DAG.

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
[`cmd/magus/packs_interp.go`](../../cmd/magus/packs_interp.go):

```go
_ ".../internal/interp/engine/buzz"
```

## The spell contract

A spell exports a fixed set of `mgs_`-prefixed functions (see
[spells.md](spells.md#authoring-a-custom-spell)). The list of optional functions
and the decoder keys they map to is **single-sourced** in
[`internal/spellruntime/contract.go`](../../internal/spellruntime/contract.go) as
`OptionalContract`, and the Buzz resolver (`internal/spellruntime/resolve.go`) iterates
that one list. A spell's `mgs_` functions decode to a `Spec` for every scalar and
list contribution (`needs`, `provides`, `claims`, `version_cmd`, `opaque`) and
for record-shaped ops (`{bin, args, charms}`).

An op is a command or a service; a function-valued op is `fun(Target) > Command` or
`fun(Target) > Service`, called once at load to record the declarative value it
returns (a Command's `{bin, args, charms}` or a Service's `{command, readiness?, stop?}`,
a long-running process `magus run` blocks on). The op's kind is inferred from that
value, so one spell mixes both. A remote cache backend
is not an op: it is a spell that exports the backend functions
(`enabled`/`get_artifact`/`put_artifact`/`prune`), detected by name and wired with
`magus\cache.remote` (see [Remote caching](cache/remote.md)).

**Doc-comment capture.** Buzz captures a handler's doc comment at compile time
(the parser binds the comment to the function node; `FunDoc` reads it back), and
`magus doctor` enforces one on each function-handler target. Note Buzz's
`Chunk.Doc` is in-memory only and not serialized to bytecode, so Buzz captures
docs only for freshly-compiled workspace `.buzz` spells, never the embedded
built-ins.

## Host modules are a superset of Buzz's stdlib

magus layers its host methods onto Buzz's own stdlib modules under the **same bare
names**: `import "os"` carries both Buzz's `os.*` (sleep, env, execute) and magus's
additions (`os\exec`, `os\which`, ...); `import "fs"` carries Buzz's `fs` plus
`fs\glob`/`readFile`; and magus adds whole modules Buzz lacks (`vcs`, `archive`,
`http`, `charm`, ...). One import per domain covers the union, with no separate
`extra` namespace to remember which side a call lives on.

Where a method overlaps a Buzz stdlib call, the magus form is **sandbox-aware**
while the bare stdlib is not. For example, `env\get`/`lookup` honor the env
allowlist, whereas Buzz's `os\env` is raw. Those overlaps are noted per-method in the
[module reference](../reference/buzz/index.md) (either works); the cross-reference lives in
`host/overlap.go`.

A few entries are _not_ treated as duplicates because the magus behavior the
stdlib can't reproduce: magus's `os\exit` raises a lifecycle error (Buzz's
hard-exits the process), magus's `os\sleep` is cancellable (Buzz's blocks), and
magus's `crypto.*_file` hashes a file (Buzz's `hash` only takes a string). These
stay on the magus surface.

A workspace spell lives at `spells/<name>/spell.buzz` (or flat
`spells/<name>.buzz`).

## "Built-in spell" vs language "builtins"

A **built-in spell** is a spell whose bytecode is compiled from
`spells/<name>/spell.buzz` and embedded in the magus binary (`go`,
`typescript`, `docker`, ...; see [spells.md](spells.md#built-in)). This is a magus
concept and is unrelated to Buzz's language **builtins** (`spawn`, list/map
methods, etc.), which are part of the Buzz language itself. The docs always write
"built-in spell" when they mean the former.

## Adding a new language

The engine interface is the stable, clean part of the seam. Plugging in a second
language today, however, also touches a handful of **hard-coded dispatch spots**
above the interface. This is the current state, not the end state:

1. Implement `engine.Engine`/`Session` for the VM and `engine.Register` it (the
   clean part).
2. Map the file extension to the engine in `engineForExt`
   (`internal/interp/source.go`) and add the glob to `scriptExts` /
   the `magusfile.<ext>` lists.
3. Branch the runtime where it special-cases an engine by name
   (`src.Engine == "buzz"` in `internal/interp/runtime.go`).
4. Provide the per-engine host bindings (the `magus.*` surface), as
   `internal/interp/bindings/buzz.go` does today.

**Future direction: registry-driven discovery.** The intent is to derive
extensions, magusfile filenames, and dispatch from the engine registry itself, so
adding a language means registering a backend (with its extensions and binding
installer) and nothing else, with no edits to `source.go`, `runtime.go`, or
switch statements. That refactor is deliberately out of scope for now. The
hard-coded spots above are the seam's known leaks, documented so they
are visible rather than surprising.

## See also

- [spells.md](spells.md): the `mgs_` spell contract and how spells compose.
- [targets.md](targets.md): the runnable unit and its CLI grammar.
- [modules/index.md](../reference/buzz/index.md): the `magus.*` host module reference.
- [cache.md](cache.md): the static glob extraction "Why Buzz" leans on.
- [dependencies.md](dependencies.md): the both-arms rule for a charm-conditional `magus\needs`.
- [sandbox.md](sandbox.md): the confinement that also bounds magusfile load-time I/O.
