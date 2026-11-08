---
title: "ADR 0035: buzz bindings come from one config-driven generator"
order: 35
description: Every Go type and host function that reaches Buzz is mirrored by one generator, buzzgen in libs/gopherbuzz, driven by a .buzzgen.yaml at the workspace root and reading Go with go/packages. Enum cases come from declared constants, callbacks and module errors are typed, untyped maps become declared structs, and cmd/magus-utils keeps none of the boundary registry or its emitters.
tags: [adr, decision, buzz, gopherbuzz, codegen, host-modules]
status: proposed
date: 2026-11-08
supersedes: the boundary registry in cmd/magus-utils and the reflect-only libs/gopherbuzz/buzzgen
---

# ADR 0035: buzz bindings come from one config-driven generator

## Context

Measured at 808419f65 on 2026-11-08 with `wc`, `grep` and `magus refs`.

### Two generators, one of them a registry

- `cmd/magus-utils/boundary_types.go` (1,257 lines) holds the registry: 413
  `boundaryTypes` entries (305 marked `RuntimeObject`, 2 marked `TS`) and 103
  `boundaryEnums` carrying 532 hand-listed `enumCase` values. Types are read with
  `reflect.TypeFor` only; nothing in cmd/magus-utils uses go/packages.
- `openStringTypes` (10 entries, each with a reason) and `checkNamedStrings` refuse a
  named string type that is in neither list.
- Ten subcommands emit Go-to-Buzz output: `types`, `tstypes`, `boundarylist`,
  `boundaryobjects`, `boundarydecode`, `contract`, `moduledecls`, `moduleset`,
  `bindings` and `libmodules`. Their files under cmd/magus-utils total 4,862 lines.
- They are invoked by 183 `//go:generate` lines in 52 files (130 `types`, 46 `bindings`,
  one each for the rest), and the root `types_generate` target in `magusfile.buzz` runs
  `go generate ./cmd/magus-utils` on top of them.
- `libs/gopherbuzz/buzzgen/mirror.go` (306 lines) is a library taking a `reflect.Type`:
  `ObjectDecl`, `FieldName`, `FieldType`, `TypeParams`, `EnumType`, with
  `Options{Namer, EnumType, TimeAsString}`. It does pluggable naming and generic type
  parameters well. It has no registry, decoders, encoders, TypeScript or named-string
  refusal, so cmd/magus-utils supplies all of those and reaches back into it.

### Untyped values at the boundary

- `std.Callback` is `Call(ctx, args ...any) ([]any, error)` (`std/module.go:27`).
  `std/fs.go` `walkStepResult` asserts `ret[0].(string)` by hand for `fs\walk`;
  `callbackTruthy` (`std/fs.go:1182`) and its copy in `callPredicate`
  (`std/charm.go:399`) each switch on a returned `any`; `OsRetry` returns `ret[0]` raw.
  Six Impls take a callback.
- About 25 Impl sites read `map[string]any` or `[]any` arguments by hand: `std/log.go`,
  `std/pipe.go`, `std/magus_handle.go` (`decodeJobPut`, `decodeJobResult`),
  `std/magus_trail.go`, `std/magus.go:1208`, `std/merge.go:104`, `std/fmt.go:32`.
- Five Impls return `any` or `map[string]any`: `MergeShallow`, `MergeDeep`, `OsRetry`,
  `PipeValue`, `MagusJobSchema`.
- The generated declarations in `internal/spell/gen/decls/*.buzz` carry 400 `!> any`
  error clauses in 42 files (magus 132, termkit 42, fs 33, charm 24) and 108 `any` in
  value positions.
- `TypeAny` and `TypeAnyMap` appear 95 times in 26 Go files. `Arg.Default` is `any`,
  set on 5 arguments, all scalars.
- `libs/result/result.buzz` `caught` takes `err: any` and narrows it by hand, because a
  host error crosses as a map.

### The first user

`types.WalkStep` (`types/walk.go`) is a named string with three constants, mirrored as
`export enum<str> WalkStep` in `fs.buzz`. Its Buzz signature is typed:
`fun walk(root: str, callback: fun (path: str, isDir: bool) > WalkStep !> any)`. Its Go
side is not: the Impl receives a `Callback`, and the `[]any` it gets back is checked in
`walkStepResult`. The enum's cases are listed a second time in `boundaryEnums`.

## Decision

These points were decided by Eli before this record and are not reopened here: one
generator named `buzzgen` in libs/gopherbuzz, driven by `.buzzgen.yaml` the way mockery
is driven by its config, reading Go with go/packages; enum cases derived from constants;
one invocation form; the registry and every emitter leaving cmd/magus-utils; typed
callbacks; generated structs for untyped maps; typed module errors; one pull request;
a deliberate break with no compat shim. This record fixes how.

### 1. The generator

- The library is `github.com/egladman/magus/libs/gopherbuzz/buzzgen`. It replaces
  `mirror.go`: the `reflect.Type` entry points are deleted, and the renderer takes a
  `go/types.Type`. `Namer` and generic type parameters carry over; `Namer` becomes
  `func(*types.Named) string`, filled from the config's `name` keys.
- The command is `libs/gopherbuzz/cmd/buzzgen`. It loads every package the config names
  in one `packages.Load` call with `NeedTypes | NeedSyntax | NeedTypesInfo`.
- It links none of the packages it reads or writes. A type error inside a file buzzgen
  itself writes is tolerated; any other load error stops it. This is the problem
  `contract.go` solves today by parsing instead of importing, solved once.
- The descriptor types `Module`, `Method`, `Arg`, `Ret`, `Namespace`, `Object` and
  `Protocol` move from `std/module.go` to a new package
  `github.com/egladman/magus/libs/gopherbuzz/host`, so an embedder declares modules in
  the same vocabulary. `std` declares `host.Module` values; `std/module.go` keeps only
  magus's module list. No alias is left behind.
- buzzgen reads the module list statically: the config names a package and a variable
  holding `[]host.Module`, and buzzgen walks its composite literals through
  `types.Info`. A field set by anything but a literal or a function identifier is
  refused with its position.
- `libs/gopherbuzz/go.mod` gains `golang.org/x/tools`.

### 2. The config

`.buzzgen.yaml` sits at the workspace root. magus's own is the first; any workspace that
embeds gopherbuzz writes its own. The version follows the Kubernetes form. The shape
lists types under the package that declares them, as mockery lists interfaces: that is
also go/packages' unit of loading.

```yaml
version: v1alpha1
error: github.com/egladman/magus/types.Diagnostic
modules:
  package: github.com/egladman/magus/std
  variable: Modules
  dynamic:
    merge.shallow: merges caller maps whose keys Go never reads
    merge.deep: merges caller maps whose keys Go never reads
packages:
  github.com/egladman/magus/types:
    types:
      Path: {}
      Target: {}
      StatusTargetRun: {name: TargetRun}
      WalkStep: {}
      ProjectOrigin:
        open: a provider-declared project carries its provider's name
  github.com/egladman/magus/internal/queue/types:
    types:
      StackMerge:
        open: the queue provider script reads it as str until its typed rewrite
outputs:
  bindings: {kind: go, path: internal/interp/bindings/gen}
  decode: {kind: go-decode, path: internal/interp/bindings/gen/decode}
  decls: {kind: buzz-decls, path: internal/spell/gen/decls}
  spell-types: {kind: buzz-types, path: internal/spell/gen/types, packages: [github.com/egladman/magus/internal/spell]}
  queue-types: {kind: buzz-types, path: internal/queue/provider/gen/types, packages: [github.com/egladman/magus/internal/queue/types]}
  console-types: {kind: typescript, path: console/src/gen/magus/types.ts}
  docs-types: {kind: typescript, path: docs/src/gen/magus/types.ts}
```

The type list above is a sample; the full list is the 413 registry entries carried over.
The keys:

| Key                            | Meaning                                                                                                                                     |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `version`                      | the schema version; anything but `v1alpha1` is an error                                                                                     |
| `error`                        | the Go type every host error crosses as; see part 6                                                                                         |
| `modules.package`, `.variable` | where the `[]host.Module` list is declared                                                                                                  |
| `modules.dynamic`              | `<module>.<function>`: reason, for a host function allowed to take or return `any`, `[]any` or `map[string]any`; a blank reason is an error |
| `packages.<path>.types.<Name>` | a Go type mirrored into Buzz; a struct becomes an object, a named string with constants an enum                                             |
| `name`                         | the Buzz name, when it differs from the Go name                                                                                             |
| `module`                       | scopes the mirror to one host module (`magus\CommandOptions` and `vcs\CommandOptions`)                                                      |
| `open`                         | the reason a named string crosses as `str`; see part 4                                                                                      |
| `required`                     | fields a decoder refuses a value without, today's `requiredMembers`                                                                         |
| `ts`                           | also write the type to every `typescript` output                                                                                            |
| `outputs.<name>.kind`, `.path` | one of `go`, `go-decode`, `buzz-decls`, `buzz-types`, `typescript`, and where it is written                                                 |
| `outputs.<name>.packages`      | for `buzz-types`, the packages whose listed types it holds                                                                                  |

Direction is derived, never configured. A type reached from an Impl parameter gets a
decoder; one reached from a return or a callback argument gets an encoder. The
`RuntimeObject` flag and the `inboundTypes` list are deleted. A struct a signature
reaches but the config does not list is an error naming the line to add, and a listed
type that nothing reaches and no `buzz-types` output holds is an error naming the line
to delete.

### 3. Enums

A listed named string type is an enum unless it carries `open`. Its cases are the
exported constants of that exact type in its package, in declaration order. A case's
Buzz name is the constant's identifier with the leading CamelCase words it shares with
the type name removed, in lower camel case: `WalkNext` of `WalkStep` is `next`,
`WorkspaceLoading` of `WorkspaceState` is `loading`. An empty remainder, or two constants
reaching one name, fails generation and names both constants. A type whose zero value
`""` has no constant is refused, naming the constant to add, so the zero case is
declared in Go rather than invented by the generator (`WorkspaceState` gains one). The
532 `enumCase` values are deleted.

### 4. Open strings

An open named string is marked in the config with `open: <reason>`, beside the listing
that mirrors it. The ten `openStringTypes` entries move there with their reasons. A
named string field that is neither listed nor reached as an enum is refused, as
`checkNamedStrings` does today.

### 5. Typed callbacks

A callback is declared by the Impl's Go parameter type, and nothing else. `Arg.Func`,
`TypeFunc` and `std.Callback` are deleted.

```go
func FsWalk(ctx context.Context, root string,
	callback func(ctx context.Context, path string, isDir bool) (types.WalkStep, error)) error
```

- The parameter is a Go func type whose first parameter is `context.Context` and whose
  last result is `error`, with every parameter named; the names become the Buzz labels.
  buzzgen refuses an unnamed parameter.
- buzzgen renders the Buzz type from it:
  `fun (path: str, isDir: bool) > WalkStep !> result\Diagnostic`.
- The `go` output writes `internal/interp/bindings/gen/callbacks.go`, one adapter per
  distinct func type. It turns the Buzz function value into that Go func, encoding the
  arguments and decoding the result with the generated decoder, so an unknown
  `WalkStep` is refused there with the cases it accepts.
- `walkStepResult` becomes a switch on a `types.WalkStep`. `callbackTruthy` and
  `callPredicate` are deleted: `fs\watch` and the charm predicate receive
  `func(ctx context.Context, ...) (bool, error)`.
- An `Extern` method has no Impl, so `Method.Extern` changes from `bool` to a nil value
  of the Go func type it binds, such as
  `Extern: (func(ctx context.Context, name string) (string, error))(nil)`, and buzzgen
  reads its signature the same way.
- The functions a script exports and Go calls, the spell contract in
  `internal/spell/contract.go` and the provider ops, are the same machinery in the other
  direction: a Go func type, and an adapter that calls the named Buzz function. The
  `contract` emitter and the provider-op half of `boundarydecode` become this.

### 6. Typed module errors

Every host error crosses as the type the config's `error` key names, for magus
`types.Diagnostic`. The trampoline converts an Impl's `error` with `errors.As`; an error
that is not one becomes a Diagnostic carrying its message and no code.

- Each declaration with `Raises` set says `!> result\Diagnostic` instead of `!> any`,
  and the decl file imports `result`. All 400 clauses change.
- `libs/result/result.buzz`'s `Diagnostic` and `Result` become a `buzz-types` output of
  `types.Diagnostic` and `types.Result`, so the hand mirror and the test holding it
  equal are deleted.
- `result\caught` takes a `Diagnostic` instead of `any`, and `narrowed` is deleted. The
  convention stands as AGENTS.md writes it: host modules are a throwing boundary, the
  caller catches at the call and returns a `Result`, and only a top-level entry throws.
- A Buzz callback is typed `!> result\Diagnostic`, so a script raising inside one raises
  a Diagnostic.

### 7. Untyped maps and `any`

- `TypeTag` is deleted. An argument's or return's type is its Impl's Go type, so
  `Arg.Type`, `Arg.Object`, `Arg.Enum`, `Ret.Type`, `Ret.Object` and `Ret.Enum` go too;
  `Arg` keeps `Name`, `Optional`, `Variadic` and `Default`, and `Ret` keeps `Nullable`.
- An Impl that reads a `map[string]any` or `[]any` takes a struct instead. Where a Go
  type exists, that type is used (`decodeJobPut` takes the job record it builds today).
  Where none exists, one is declared in `types/`. buzzgen generates its Buzz object,
  decoder and encoder.
- `any`, `[]any` and `map[string]any` in an Impl signature are refused unless the
  function is under `modules.dynamic`, where they render as Buzz `any`, `[any]` and
  `{str: any}`. The starting list is `merge.shallow`, `merge.deep`, `fmt`'s variadic,
  `log`'s attrs, `os.retry` and `pipe.value`, each with its reason; every other site in
  the inventory gets a struct.
- `Arg.Default` becomes a `string` holding a Go constant literal of the argument's type,
  such as `"0o755"` or `"\" \""`. buzzgen parses it with go/constant against the derived
  type, writes it into the Buzz declaration and the trampoline, and refuses a literal
  that does not fit or a default on a non-scalar argument. The five defaults convert.

### 8. Where outputs live, and the one invocation

Every output keeps its current path; each is already in a `gen/` directory. The one new
file is `internal/interp/bindings/gen/callbacks.go`. Each generated file's header names
buzzgen and the output.

Every target that writes a buzzgen output runs one command form:

```sh
go run github.com/egladman/magus/libs/gopherbuzz/cmd/buzzgen --config .buzzgen.yaml --output <name>
```

The root `types_generate` runs it for `bindings`, `decode`, `decls`, `spell-types` and
`queue-types`; console's and docs' `types-generate` run it for their own TypeScript
output, so each project writes only its own tree. All 183 `//go:generate` lines for
these emitters are deleted, and `types_generate` no longer runs `go generate`.

### 9. The deliberate break

One pull request carries the change. It changes Buzz-visible signatures (every
`!> any`, every callback type, the untyped arguments) and deletes `buzzgen`'s reflect
entry points, `std.Callback`, `TypeTag` and the registry. No compat shim, dual reader or
deprecation alias is added. The API lock files (`std/gen/std-api.lock`,
`internal/interp/bindings/testdata/magus-api.lock`) are regenerated in the same pull
request, and any comparison gate that reports the changed signatures gets one line per
finding in its `.deviations.jsonl` naming this record as the reason, so the pull request
is green without switching a gate off.

| Item                                                                                              | State     |
| ------------------------------------------------------------------------------------------------- | --------- |
| `types.WalkStep` and the typed `fs\walk` Buzz signature                                           | done      |
| `libs/gopherbuzz/buzzgen` renders from `go/types`; reflect entry points deleted                   | not built |
| `libs/gopherbuzz/cmd/buzzgen` loads with go/packages and reads `.buzzgen.yaml` `v1alpha1`         | not built |
| `libs/gopherbuzz/host` holds the module descriptor types; `std` declares `host.Module`            | not built |
| `.buzzgen.yaml` at the root carries the 413 types and 10 open strings                             | not built |
| Enum cases derived from constants; 532 `enumCase` values deleted                                  | not built |
| Typed callbacks and `gen/callbacks.go`; `std.Callback`, `callbackTruthy`, `callPredicate` deleted | not built |
| Spell contract and provider ops generated as typed exports                                        | not built |
| `!> result\Diagnostic` on every raising declaration; `result\caught` typed                        | not built |
| `TypeTag` deleted; untyped Impl arguments and returns become structs or `modules.dynamic`         | not built |
| `Arg.Default` is a typed literal string                                                           | not built |
| One `buzzgen --output` command per target; 183 `//go:generate` lines deleted                      | not built |
| cmd/magus-utils holds no boundary registry and none of the ten emitters                           | not built |

### Principles checked

- [Delete before you add](../design/principles/delete-before-you-add.md): the registry's
  enum cases, `RuntimeObject`, `inboundTypes`, `TypeTag`, `Callback`, two truthiness
  switches and the hand mirror in `libs/result` are deleted, not wrapped.
- [One source, every door](../design/principles/one-source-every-door.md): the Go
  declaration is the one source; the Buzz decls, decoders, encoders and TypeScript are
  read from it, and the config holds only what Go cannot say.
- [The checker catches it; the runtime is the backstop](../design/principles/the-checker-catches-it.md):
  a callback of the wrong shape, an error of the wrong type and a map with a misspelled
  key become check-time refusals instead of `[]any` assertions at the call.
- [No silent defaults](../design/judgment/no-silent-defaults.md): the generator invents no
  zero case, no Buzz name and no direction; an unlisted struct, an unmarked named
  string, an untyped argument outside `dynamic` and a blank reason are errors.
- [Behavior is code, facts are data](../design/principles/behavior-is-code-facts-are-data.md):
  `.buzzgen.yaml` holds facts (names, reasons, paths); what crosses and how is read from
  the Go code itself.
- [Clear is better than clever](../design/principles/clear-is-better-than-clever.md): one
  command form and one way to declare a callback, a func type in the Impl signature.
- [No generated code in the build path](../design/principles/no-generated-code-in-the-build-path.md):
  relied on, not bent. buzzgen writes only outputs a target the workspace declared
  names, and a magusfile author never runs it; an embedder that runs it chose to, the
  way it chooses mockery.
- [Abstract at the right layer, or not at all](../design/principles/abstract-at-the-right-layer.md):
  the module descriptor moves to the layer every embedder reaches, libs/gopherbuzz,
  instead of staying in magus's `std` where only magus can use it.
- [Break now, hold still later](../design/principles/break-now-hold-still-later.md) and
  [A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):
  the Buzz-visible signatures change without a migration path, and the pull request
  carries its regenerated locks and its deviation lines.
- [Flags, config and help from one source](../design/principles/flags-config-help-one-source.md):
  not bent. The rule covers magus's command tree; buzzgen is a gopherbuzz command
  outside it, with two flags.

## Alternatives

### Keep reflect and extend the registry

Add typed callbacks and errors to the existing `reflect.TypeFor` registry in
cmd/magus-utils. It lost because reflection cannot see constant declarations, so enum
cases stay hand-listed (532 of them), and it cannot see parameter names, so callback
labels stay a hand-written `Func` string. It also keeps the generator in a magus-only
program no embedder can run. Reopen if go/packages loading costs more than the
`go generate` run it replaces by a measured margin on this tree.

### Keep `std.Module` in magus and give buzzgen a magus plug-in

The library stays generic and magus supplies a reader for its own descriptor. It lost
because the plug-in is the registry again, in a new place, and an embedder would write
its own descriptor and reader from nothing. Reopen if a second embedder needs a
descriptor `host.Module` cannot express.

### Declare host functions in `.buzzgen.yaml`

List every module function, its Impl, `Raises` and defaults in YAML, as mockery lists
interfaces. It lost because every method declaration in `std` would move out of Go, away
from the Impl it describes, and a typo would surface at generation instead of as a Go
compile error. Reopen if the composite-literal reader in part 1 proves too fragile to
keep.

### Directive comments on Impls

Mark `//buzz:raises` and `//buzz:default perm=0o755` on each function and drop the
descriptor. It lost because `Namespace`, `Object`, `Protocol` and `Extern` have no
function to annotate, and comments are not type-checked. Reopen only with a Go language
feature that types annotations.

### Mark open strings in Go

A `//buzz:open <reason>` comment or a marker method on the type. It lost because it puts
Buzz knowledge in `types/`, which TypeScript and Go readers share, and because an
embedder cannot annotate a type from a module it does not own. Reopen if the config's
`open` entries are measured drifting from the types they name.

### Named func types for every callback

Require `type WalkFunc func(...)` in `types/` for each callback. It lost because the six
callback sites would gain six types used once each, and a func type in the Impl
signature already carries the names. Reopen when two Impls share a callback shape and a
reader would want one name for it.

### Host functions return `result\Result::<T>` instead of raising

The errors-as-values convention applied to the host boundary itself. It lost because
every host call in repository Buzz would change from a value to a result to unpack, the
convention already names host modules a throwing boundary to catch at the call, and
`!> result\Diagnostic` hands the catch the same typed value. Reopen if Buzz gains a
propagation operator that makes unpacking a Result as short as a call.

### Move every output under one `gen/buzzgen/` directory

It lost because every output already sits in a `gen/` directory owned by the project
that reads it, and moving them changes import paths for no reader. Reopen if a project
ends up holding a buzzgen output it does not read.

### Run buzzgen once for every output

One root invocation writing the TypeScript too. It lost because console and docs own
their `src/gen` trees, and a root target writing into them breaks per-project write
ownership. The cost is three go/packages loads instead of one. Reopen if the three loads
are measured to dominate `types_generate`.

## Consequences

- An embedder gets the whole pipeline by writing a `.buzzgen.yaml` and declaring
  `host.Module` values; magus is the first such workspace, not a special case.
- A host Impl is plain Go: typed arguments, typed callbacks, `(T, error)` returns. Its
  unit test calls it with a Go func and needs no fake `Callback`.
- `std`, every module file in it, `internal/interp/bindings`, `internal/spell`,
  `libs/result`, console, docs and the root magusfile all change in one pull request. It
  is large, and most of its review is the diff of the generated decls.
- Repository Buzz that catches a host error as `any` and reads it as a map changes to a
  typed `Diagnostic` catch.
- The descriptor's composite literals must stay literal; a module built by a loop or a
  helper call is refused.
- cmd/magus-utils keeps its other subcommands (`mcptools`, `jobschema`, `config`,
  `cliflags`, `completions`, `api`, `goapi` and the release tools).

## Open questions

- Whether gopherbuzz's checker resolves an imported object in an extern declaration's
  error clause (`!> result\Diagnostic` in a decl file that imports `result`). If it does
  not, the checker is fixed in this pull request to match upstream Buzz; no untyped
  clause is kept.
- Whether any of the 305 `RuntimeObject` entries is reached by no signature, only by an
  untyped binding. Each one found gets the typed signature that reaches it, or is
  deleted.
- How a mirrored Go type reaches a C API (raised by Eli, 2026-11-08). A Go host
  function that calls C through cgo or purego needs nothing from buzzgen: its Impl is
  typed Go. Buzz calling C through `zdef` reaches only scalars, `char*`, `void*` and two
  fixed structs today (`libs/gopherbuzz/ffi.go`). The proposal is a layered `ffi`
  setting: buzzgen derives the C layout of the Go struct from go/types and
  `CTypeLayout`, writes its zdef struct declaration beside the mirror, and refuses a
  field with no C layout by name. It needs zdef struct support in gopherbuzz matching
  upstream Buzz first.
- How a large Go slice crosses (raised by Eli, 2026-11-08). Every conversion copies
  eagerly: `ByteSliceVal` and `StringSliceVal` (`internal/interp/bindings/ffi/convert.go`)
  box each element into an 8-byte `vm.Value`, so 32 MiB of `[]byte` becomes a 256 MiB
  list, and nothing caps it. The one 32 MiB limit in the tree is the HTTP upload chunk
  (`internal/interp/bindings/http_bytes.go`), which keeps the bytes in Go. Eli asked for
  the systemic fix in gopherbuzz, not a workaround at each site. The proposal uses two
  constructs upstream Buzz already has, so no language type is added. `[]byte` crosses
  as upstream `buffer\Buffer` (`libs/gopherbuzz/std/buffer.go`), adopting the Go slice
  without a copy; its `ptr()` is also what a `zdef` function takes, which answers the C
  question above for bytes. A Go `iter.Seq[T]` crosses as a Buzz fiber, `fib<T, void>`,
  read with `foreach` as it yields, so a host function with a large result returns an
  iterator. An eager `[]T` stays a list.

## Amendments

### 2026-11-08: settings layer, and cases are named for their values

Measured against the 103 hand-listed enums, part 3's naming rule matched few of them,
and about 85 types declare no constant for `""` while every hand list names that case
`none`. Two changes, so every Buzz-visible name stays as it was:

- A case is named for its value in lower camel case (`executable-bit` is
  `executableBit`). A value that is not a word or a dashed run of words, such as a media
  type, is named for its constant less the leading words it shares with the type
  (`ImageMediaTypeManifest` is `manifest`).
- Every enum has a zero case named `none`, first, whether or not Go declares a constant
  for `""`. A type sets `zeroCase` to rename it, or to `""` to declare the enum without
  one (9 do, among them `LogLevel` and `WalkStep`).

Settings layer, as Eli asked: keys at the top level are every package's defaults, a
package's `config:` overrides them, and a type's `config:` overrides its package. The
keys are `name` (type only), `module`, `open`, `required`, `ts`, `zeroCase`, `cases`
(value to case name, merged by key) and `pkgname` (the Go package a `go` or `go-decode`
output declares, which an output's own `pkgname` overrides). A derived name that is a
Buzz keyword is refused and must be set under `cases` (`SpawnKind`'s `continue`).

With this rule 22 of the 103 enums need any setting. Two hand lists had drifted from Go:
`TargetRunState` lacked `permitted` and `VCSCapability` lacked `checkoutFastForwarder`
and `branchHeadReporter`, so the derived enums gain them. `types.TargetRunState.Values`
lacked `permitted` too, so `Valid` refused a state the server writes.
