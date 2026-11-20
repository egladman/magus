---
title: "ADR 0055: a Buzz Result names its error type"
order: 55
description: "result\\Result becomes result\\Result::<T, E>, with the error type named at every use and no default. Errors stay values as ADR 0054 has them. Diagnostic and its fail and caught helpers move out of the std result module into a magus module, the name for Eli to pick. CallOp reads the error of the one E the boundary carries. The 2,000-odd call sites convert by codemod, and value! moves onto methods."
tags: [adr, decision, buzz, errors, result, boundary, providers]
status: accepted (Diagnostic module name open)
date: 2026-11-19
---

# ADR 0055: a Buzz Result names its error type

## Context

[ADR 0054](0054-a-spell-op-returns-a-result.md) made a failing spell op return a
`result\Result::<T>`, and `bindings.CallOp` reads its error as the failure. The Buzz type
is one object with a fixed error:

```buzz
export object Result::<T> {
    value: T? = null,
    error: Diagnostic? = null,
}
```

The error is always a `Diagnostic` (`code`, `message`, `url`, `fix`). A Buzz script that
needs a different error shape has no way to say so: it either wraps its error as a
Diagnostic or returns a second type of its own. Callers then unwrap by hand. Eli ruled on
2026-11-19 that errors stay values, so ADR 0054 holds and no `!>` is typed, and that the
Result names its error type: `result\Result::<T, E>`.

The forces, measured at `0ef0dd64a`. The counts below exclude `docs/`, Markdown and
`testdata/`. The method is a `git grep -o` over the tree with those paths excluded; a
recount at another base will move them.

- `.value!` force-unwraps: 1,972.
- Reads of `.error` against `null` (`!= null` or `== null`): 657.
- `result\ok` calls: 1,422. `result\fail` calls: 562. `result\caught` calls: 763.
- `Result::<` type signatures in `.buzz` files: 2,024.

A probe run on 2026-11-19 counted a different sample (2,124 `.value!`, 656 `.error` checks,
1,415 `ok`, 555 `fail`, 755 `caught`). Its paths differ from the method above, so this record
quotes the counts above and marks the difference as an open question.

Four probes on the gopherbuzz build of 2026-11-19 shape the decision:

- An explicit error type that does not match the value fails: `Result::<int, D>{ error = "str" }`
  is refused with BZZ1005.
- Generic methods on generic objects work: `map::<U>` on a `Result::<T, E>` type checks.
- A generic type alias does not: `type Res::<T> = ...` fails with BZZ1002. There are no
  generic aliases.
- A default type parameter parses but is not enforced: `object R::<T, E = D>` accepts
  `R::<int>{ error = "not a D" }` and runs. A default is a declaration nobody checks.

The TypeScript mirror already takes an error parameter. `internal/generate/emit/tstypes.go`
renders `Result<T, E extends Error = MagusError>`. The Go mirror, `types.Result[T]`, has no
error parameter and holds `*Diagnostic`. The generator keeps the two in step, and this ADR
changes the Buzz side only.

## Decision

### The shape

`result\Result::<T, E>` is the Buzz form of a value or the error that stopped it, with both
parameters named at every use:

```buzz
export object Result::<T, E> {
    value: T? = null,
    error: E? = null,
}
```

Exactly one side is meaningful. A Result with neither side set is a malformed answer, as
ADR 0054 already states.

The module declares no default for `E`. A default would read as a promise the checker does
not keep (see the probe above), so every signature and every constructor names `E`.

### The methods

Methods on the object replace the reads that `.value!` and `.error` did by hand:

| method | signature | returns |
| --- | --- | --- |
| `ok` | `fun ok() > T?` | the value when there is no error, otherwise null |
| `orElse` | `fun orElse(fallback: T) > T` | the value, or `fallback` when there is an error |
| `map` | `fun map::<U>(fn: fun (value: T) > U) > Result::<U, E>` | a Result of `fn` applied to the value, the error passed through |
| `then` | `fun then::<U>(fn: fun (value: T) > Result::<U, E>) > Result::<U, E>` | `fn`'s Result when there is a value, the error passed through |
| `mapError` | `fun mapError::<F>(fn: fun (error: E) > F) > Result::<T, F>` | the value passed through, `fn` applied to the error |

`ok` is a method and also a module function (`result\ok`). A dot reaches a value and a
backslash reaches a module (see the conventions page on Buzz syntax), so both names stay.

`map` and `mapError` are generic over the method's own parameter (`map::<U>`), which the
probe showed works. Nothing here uses a generic alias or a default type parameter.

The module-level constructors become generic over both parameters:

```buzz
export fun ok::<T, E>(value: T) > Result::<T, E>
export fun fail::<T, E>(error: E) > Result::<T, E>
```

### Where Diagnostic and its helpers go

`Diagnostic` (`code`, `message`, `url`, `fix`) and the two helpers `fail` (a Diagnostic with a
message and no code) and `caught` (a catch's value, narrowed to a Diagnostic) are magus
concepts. The principle `buzz-modules-live-in-their-layer` (PR #867, not yet on `main`, so it is not
linked here) says a std module names no magus concept, and the current
`libs/result/result.buzz` names `Diagnostic` and `types.Diagnostic` directly. So the
Diagnostic moves out of the std `result` module.

Proposed: `magus/diagnostic`, holding `Diagnostic`, `fail` and `caught`, with the same
fields and the same narrowing `caught` does now. The name is for Eli to pick. Candidates:
`magus/diagnostic`, `magus/failure`, `magus/errors`. This record uses `magus/diagnostic`
throughout so the text reads cleanly, and the name is an open question below.

The std `result` module keeps `Result::<T, E>`, the methods and the generic `ok` and `fail`.

### What CallOp reads

`bindings.CallOp` reads a provider op's return as a `Result::<T, E>`. The boundary carries one
error type: `magus/diagnostic.Diagnostic`. The reader is unchanged for it:
`readDiagnostic` reads the fields and `diagnosticError` builds the coded error that
`errors.Is` matches.

Any other `E` is refused at load, not read at run time. `checkContract` already refuses an op
that declares `!>` (MGS1051); the same check refuses an op whose declared `Result` names a
type other than `Diagnostic`, with a new code the implementation will assign. Refusing at load
matches the rest of the contract: the op's signature is the statement, read from the source,
so the check never depends on a default the checker does not enforce.

Inside a script, `E` can be any type. The restriction applies to a value that crosses to the
host, where the Go reader needs a fixed shape.

### The Go and TypeScript mirrors

- Go: `types.Result[T]` keeps its `Error *Diagnostic` field. A Buzz `Result::<T, Diagnostic>` maps
  onto it, and the generator says so in a comment. No Go type gains a parameter, because nothing
  Go-side needs an `E` the boundary does not carry.
- TypeScript: the mirror already has `E extends Error = MagusError`. It stays as it is. The
  generator emits the same `E` for a boundary Result, so a TS caller and a Buzz caller name the
  same error at the same place.

### The codemod

Every use converts by a single tool run over the `.buzz` tree, one change per shape:

- `result\Result::<T>` becomes `result\Result::<T, diagnostic\Diagnostic>`, and the import of
  `magus/diagnostic` is added where the file uses it.
- `result\ok::<T>(v)` becomes `result\ok::<T, diagnostic\Diagnostic>(v)`.
- `result\fail::<T>(msg)` becomes `diagnostic\fail::<T>(msg)`. A failure with a code keeps
  building its Diagnostic in place, as it does now.
- `result\caught::<T>(err, doing: d)` becomes `diagnostic\caught::<T>(err, doing: d)`.

The codemod runs as one commit and the changelog records the break. A file that names a
non-Diagnostic `E` after the codemod is an error the checker reports, which is the point of the
change.

### The `.value!` force-unwraps

Most of the 1,972 `.value!` sites follow an `.error` check in the same scope. They convert to
the method that states what the branch does:

- After an error check that returns, `r.value!` becomes `r.ok()!`. The `!` stays because `ok()`
  returns `T?`, and the check above already proved it is set.
- A site that needs a fallback becomes `r.orElse(fallback)`.
- A chain that transforms the value becomes `r.map(...)` or `r.then(...)`, so the error path
  stays in the chain and no branch reads it by hand.

There is no `unwrap` method. A method that raised on an error would reintroduce a raise through
a host call, which ADR 0054 refused.

### The unread-Result lint

A lint flags a call that returns a `Result` whose value is never read: a binding that is never
used, or a statement whose Result is discarded. The checker already warns on the discard
(BZZ3004, "discarded value of type `Result::<bool>`"), which the earlier lint run printed. The
new lint covers the case BZZ3004 does not: a Result bound to a name and never read. It is
proposed, not built, and lives in `hack/lint` beside `no-throw`.

### Principles checked

- [No silent defaults](../design/judgment/no-silent-defaults.md): the default type parameter
  gopherbuzz accepts and does not enforce is the silent default this rule forbids. Nothing relies on
  it, and the declaration is not written.
- [Provider I/O is Buzz](../design/judgment/provider-io-is-buzz.md): the op still owns its
  vendor's words. Go reads only the shape of the error, the same reader ADR 0054 gave it.
- [One source, every door](../design/principles/one-source-every-door.md): `Diagnostic` keeps one
  definition, in the magus module. The Go and TypeScript mirrors are generated from it, and the
  codemod changes each call site once.
- [A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):
  the codemod is a break across about 4,800 call sites (2,024 signatures, 1,422 `ok`, 562 `fail`
  and 763 `caught`, by the counts in Context) and a new refusal at load. It merges as one
  change, with the changelog naming the break and the codemod that converts it.
- [Enforcement, not pinky promises](../design/principles/enforcement-not-pinky-promises.md): the
  "every use names `E`" rule is enforced at load and by the checker, not by convention.
  The unread-Result lint enforces the "read every Result" rule.
- [Clear is better than clever](../design/principles/clear-is-better-than-clever.md): the methods
  name what a branch does, `ok`, `orElse`, `map`, `then`, `mapError`, where `value!` said nothing
  about the error path.
- Buzz module layers (PR #867, not yet on `main`): a std module names no magus concept, so
  `Diagnostic` moves out of `result`. Cited pending until that page lands on `main`.

## Alternatives

### A protocol-typed error field

The error field could be typed as a protocol or interface carrying `code` and `message`, so `E`
would be open and every error would satisfy one shape. Eli did not pick this. This record does not
weigh it beyond the ruling. It is the path to reopen if gopherbuzz gains interfaces that the
boundary can check at load, because it would let `E` stay open without a fixed Diagnostic.

- Lost because: the ruling picked a named `E` with no protocol.
- Reopen when: the language has a protocol the boundary can check at load without a default.

### Typed `!>` errors

A function could declare what it raises, `!> E`, and drop the Result wrapper for raises. Eli ruled
errors stay values, not typed `!>`.

- Lost because: ADR 0054 refused a raise through a host call, and a typed raise is still a raise.
- Evidence: the 2026-11-19 ruling; ADR 0054's no-throw rows.
- Reopen when: a host call can carry a typed raise without the `CallOp` read that ADR 0054 relies on.

### A default type parameter, `E = Diagnostic`

The probe showed `object R::<T, E = D>` parses. It would let most sites write `Result::<T>` and keep
the diff small.

- Lost because: the probe showed the default is not enforced. `R::<int>{ error = "not a D" }` runs.
  A default that does not hold is a silent default, which the no-silent-defaults judgment forbids.
- Evidence: the 2026-11-19 probe.
- Reopen when: gopherbuzz enforces a default type parameter at the constructor.

### Generic type aliases, `type Res::<T> = Result::<T, Diagnostic>`

An alias would give the common case a short name.

- Lost because: the probe showed `type Res::<T> = ...` fails with BZZ1002. There are no generic
  aliases.
- Evidence: the 2026-11-19 probe.
- Reopen when: gopherbuzz accepts a generic alias.

### Keep `Result::<T>` with a fixed Diagnostic error

Nothing changes. Every error is a Diagnostic, and a script that needs another shape wraps it.

- Lost because: a script that carries a non-Diagnostic error wraps it by hand today, and `map`
  and `mapError` over another error type have nowhere to go. The tree is not yet measured for
  how many such scripts exist; that count is an open question below.
- Evidence: the counts above; the `caught` helper narrows four error shapes into one Diagnostic by
  hand.
- Reopen when: no script in the tree needs an error other than a Diagnostic.

## Consequences

- Every `.buzz` file that names a Result changes, through the codemod. The break merges as one
  change with its changelog entry.
- The std `result` module no longer names `Diagnostic`. A script that imports `result` for
  `caught` moves to `magus/diagnostic`.
- An op that declares a `Result` over a type other than `Diagnostic` is refused at load, with a
  code the implementation assigns.
- Each `.value!` site converts to a method, and `ok()` or `orElse` names the branch's intent.
- Go code and the TypeScript mirror do not change shape. The TypeScript mirror already takes `E`.
- The unread-Result lint is a new rule, and a new code if it reports.

## Open questions

- **The Diagnostic module name.** Eli picks among `magus/diagnostic`, `magus/failure` and
  `magus/errors`. Every example here uses `magus/diagnostic`.
- **The load refusal code.** The refusal for an op whose `Result` names a non-Diagnostic `E`
  needs a code. The implementation assigns it, and this record names none.
- **The methods' names.** `ok()` on an object shares a name with the module function `ok`. The
  two live in different namespaces, but Eli should confirm the name before the codemod runs.
- **The unread-Result lint.** Whether it is a `hack/lint` rule or a checker warning. This record
  proposes `hack/lint`, beside `no-throw`.
- **The `.value!` migration rule.** The record covers the three shapes that dominate. A site that
  matches none of them is a case-by-case edit, and the codemod does not touch it.
- **The counts.** This record quotes the counts above with their method. The 2,124 figure from the
  probe uses a different sample. A recount at the base the codemod runs on will decide which the
  changelog cites.
