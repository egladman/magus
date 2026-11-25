---
title: "ADR 0058: buzz std natives are private externs"
order: 58
description: A gopherbuzz std module declares its Go natives as non-exported extern funs in its own .buzz file, as upstream Buzz does, bound from a private table registered under the module's own name. The four *core helper modules go, and their import names stop resolving.
tags: [adr, decision, buzz, gopherbuzz, stdlib, modules]
status: accepted
date: 2026-11-24
---

# ADR 0058: buzz std natives are private externs

## Context

Four gopherbuzz std modules reach their Go natives through a helper module of their
own: `crypto` imports `cryptocore`, `serialize` imports `serializecore`, `io` imports
`iocore`, and `assert` imports `assertcore`. Each helper is a module like any other.
It resolves as an import name in every script, so any program can import
`serializecore` and call a native its owning module meant to wrap.

The helper also has to appear wherever its owner is allowed. The MCP Buzz tools
provide a pure subset of the stdlib (`pureStdlib`, `internal/interp/bindings/modules.go`),
and that list names `serializecore` beside `serialize`, because `serialize` fails to
import without it. That entry arrived in pull request 888 as a stopgap. Every sandbox
allowlist written after it would have to learn the same pairs.

Upstream Buzz has no such modules. A module there declares its natives in its own
source as non-exported `extern fun` declarations marked `/// @private`, and the
runtime binds them for that module alone. gopherbuzz matches upstream's
language-visible behavior and diverges only for a concrete reason
([std README](https://github.com/egladman/magus/blob/4639cf23c0f3b4aec0285379614fbcdd51f6e1fd/libs/gopherbuzz/std/README.md)). A helper module is a divergence
with no such reason. Every ecosystem with real module privacy keeps a module's
helpers inside its boundary.

## Decision

| behavior                                                                                                                                                                    | enforcement                     | State   |
| --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------- | ------- |
| `serializecore`, `cryptocore`, `iocore` and `assertcore` are deleted, all four in one change.                                                                               | the import names stop resolving | planned |
| A std module declares its natives as non-exported `extern fun` with `/// @private` in its own `.buzz` file.                                                                 | gopherbuzz std sources          | planned |
| The natives bind from a private table registered under the module's own name, through a session call distinct from `SetNativeModule`. The binding is scoped to that module. | `libs/gopherbuzz` session API   | planned |
| A private extern takes upstream's capitalized naming: `BoxedWrap`, `JsonDecode`, `FileOpen`.                                                                                | gopherbuzz std sources          | planned |
| Two modules that need one native share it in Go. A shared Buzz helper module is refused.                                                                                    | review                          | planned |
| `pureStdlib` names public modules only, and a closure test asserts that every import of a provided module is itself provided.                                               | a test beside `PureStdlib`      | planned |
| `serialize` is reconciled with upstream in the same change: it exports `serializeValue` and imports `buzz:buffer` and `buzz:std`.                                           | gopherbuzz std sources          | planned |
| `Module.Labels` stays.                                                                                                                                                      | `libs/gopherbuzz`               | done    |

The private table is keyed by the module that declares the externs, so an
`extern fun` in one module never binds a native registered for another. A script
that names one of the four helper modules fails to import, which is the break
this decision records.

Bytecode-cache replay has to restore the private bindings. A cached module that
replays without them would call an unbound extern, so the change carries a test
that runs a module with private externs from the cache.

### Principles checked

- [A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):
  the four import names stop resolving with no alias, and the changelog records it.
- [Delete before you add](../design/principles/delete-before-you-add.md): four modules
  and an allowlist entry go; one session call comes in.
- [No silent defaults](../design/judgment/no-silent-defaults.md): an unbound private
  extern fails by name, and the closure test fails when an allowlist omits a module's
  import.

## Alternatives

Keep the helper modules and hide them from import resolution with a label. A label
filter would have to run in every resolver and every allowlist, which is the
duplication this decision removes, and the modules would still differ from
upstream's shape. Revisit if upstream Buzz adds helper modules of its own.

Register natives as exported members of the public module from Go. The checker sees
no signatures on a module built as a Go value, which is why `crypto.buzz` declares
its enum and wrapper in Buzz today. Typed signatures need the Buzz declaration, and
upstream already has one: the private `extern fun`.

Share a Buzz helper among modules that need one native. It brings back an import
name that every allowlist must carry. Sharing the Go function costs nothing at the
language level.

## Consequences

A standalone script that imported a `*core` module stops compiling and moves to the
public module's exports.

An allowlist reads as the modules a caller may use, with no hidden pairs, and the
closure test catches a provided module whose imports are withheld.

gopherbuzz std sources read like upstream's, so a module ported from upstream needs
no rewiring of its natives.

## Open questions

Whether `buzz.LabelGopherbuzz` still marks `assert`, `suite` and `testing` once
`assertcore` is gone is settled when the change is written. `Module.Labels` stays
either way.
