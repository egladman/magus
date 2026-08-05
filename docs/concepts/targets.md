---
title: Targets
order: 2
description: A Target is the addressable unit of work in magus, keyed by project Path and operation Name, with charms layered on top for execution modifiers.
tags: [targets, build, projects, cli, grammar, spells, charms, magusfile]
---

# Targets

A **Target** is the addressable unit of work in magus: _what project_ (Path) and _what operation_ (Name). Everything else (shards, spell filters, charms) is configuration layered on top, not part of the Target's identity.

A target is _what_ you run (`magus run lint`); a spell is _how_ a tool does it. See [Spells vs Targets](spells.md#spells-vs-targets).

## The struct

```go
type Target struct {
    Path   string   // project path relative to workspace root
    Name   string   // the target name; the operation to run
    Charms []string // execution charms (modifiers); see docs/charms.md
    Files  []string // changed files; populated only by ExpandAffected
}
```

`Path` and `Name` are the two durable identities. `Charms` modify _how_ the target runs (see [charms.md](charms.md)). `Files` is metadata populated automatically by the VCS-affected engine; nil for explicitly constructed targets.

## CLI grammar

```text
[spell::]op-or-name[:charm,...]  [project ...]
   │       │           │              │
   │       │           │              └── projects, positional (omit = cwd/all)
   │       │           └───────────────── charms, comma-separated modifiers
   │       └───────────────────────────── a target name, or (with spell::) a spell op
   └───────────────────────────────────── spell, CLI-only op-direct qualifier
```

The project is a **positional** argument, not embedded in the target token. `:` introduces [charms](charms.md); `spell::op` invokes one spell's op directly (see [spell-qualified targets](#cli-extension-spell-qualified-targets)).

Examples:

| Command                     | Project(s) | Name           | Charms        | Spell |
| --------------------------- | ---------- | -------------- | ------------- | ----- |
| `magus run build`           | cwd / all  | `build`        | -             | -     |
| `magus run test api`        | `api`      | `test`         | -             | -     |
| `magus run format:rw api`   | `api`      | `format`       | `rw`          | -     |
| `magus run lint:rw,debug /` | all        | `lint`         | `rw`, `debug` | -     |
| `magus run golang::go-test`     | all        | `go-test` (op) | -             | `go`  |

Invalid forms:

| String            | Reason                                                   |
| ----------------- | -------------------------------------------------------- |
| `""`              | empty target rejected                                    |
| `lint:`           | charm must not be empty                                  |
| `web/studio:test` | `/` not allowed in a target name (project is positional) |

The canonical serialized form of a resolved Target (what `Target.String()` emits and what `describe`/logs show) is `path:name` (e.g. `web/studio:test`). That is the output form; the grammar above is how you type it.

## Path resolution on the CLI

`Path` is stored **relative to the workspace root**, but the CLI accepts project names from anywhere in the tree. Arguments to `magus run`, `magus list`, and `magus clean` resolve as follows:

| Input                        | Resolved against           | Example (cwd = `web/studio`) |
| ---------------------------- | -------------------------- | ---------------------------- |
| bare (`api`, `web/studio`)   | workspace root             | `api` → `api`                |
| dot-relative (`./x`, `../x`) | current working directory  | `../api` → `web/api`         |
| `.`                          | the project containing cwd | `.` → `web/studio`           |
| empty or `/`                 | all projects               | n/a                          |

So `../foo` behaves as a shell user expects: from `web/studio`, `magus run build ../foo` targets `web/foo`. Bare paths stay workspace-relative regardless of cwd. magus rejects two inputs:

- **Absolute paths** (`/etc`, `C:\foo`): project paths must be repo-relative.
- **Paths that escape the workspace root** (`../../outside`): magus never operates outside the workspace it discovered.

Resolution is implemented by `internal/file/path.Resolve(input, anchor)`, where the anchor is the cwd expressed relative to the workspace root. The same helper backs `WithDependsOn` in a magusfile, so dependency paths and CLI arguments obey identical rules.

### Symlinks

The workspace root is canonicalised (symlinks resolved) at discovery, and the sandbox enforces access against **real, resolved paths** on Linux via the kernel landlock LSM. A symlink inside the workspace pointing at `/etc` grants no access to `/etc`.

Two consequences:

- **Symlinked directories are not discovered as projects.** Discovery does not follow symlinks; a symlinked directory is silently skipped.
- **Workspace-escaping symlinks are a hard error.** `magus doctor` fails if it finds a symlink whose resolved target lands outside the workspace root. On platforms without landlock (macOS, Windows, kernels < 5.13) such a link is the only path by which a spell subprocess could reach outside the tree, so it is treated as a `fail`, not a warning.

## The target name

A target name is typically one of the eight canonical operations (see below); custom names are allowed for work with no canonical home. The type is `project.Target` (a `string` alias).

| Name        | Meaning                                              |
| ----------- | ---------------------------------------------------- |
| `preflight` | pre-run checks (workspace health, missing tools)     |
| `build`     | compile / produce artifacts                          |
| `test`      | run the test suite                                   |
| `lint`      | static analysis, type-check                          |
| `format`    | format source files                                  |
| `security`  | security scanners (vulnerability/dependency audits)  |
| `clean`     | remove local build artifacts                         |
| `generate`  | run code generators                                  |

There is also `ci`: an ordinary magusfile-defined target, not a hardcoded chain - you compose its stages yourself with `magus\needs`. `Magus.RunCI` treats it specially in exactly three ways: it strips the `rw` charm (ci always runs read-only), it is the anchor `magus affected ci` and `magus affected --plan` key off, and it must not silently no-op - a selected scope with no project declaring `ci` is a load error (see [dependencies.md](dependencies.md)), not a quiet success.

Tool operations compose **into** these targets; they are not targets of their own. All static analysis - `go-vet`, `golangci-lint`, `cargo-clippy`, type-checks - belongs under `lint` (its definition is "static analysis, type-check"), not a bespoke `vet` or `typecheck` target. Security scanners (`govulncheck`, `pip-audit`, `cargo-audit`, `pnpm audit`, `trivy`) compose into `security` - a phase of its own, not a lint fragment, because its verdict is not a pure function of the tree: a scanner consults a live advisory database, so a newly published CVE flips the result with no input change. That time dependence is exactly what makes it wrong to cache under `lint`'s rules; a `security` target typically declares `skip_cache` (see [cache.md](cache.md#opting-out-and-busting)). `audit` is the same phase under another name - declare it as `security` (MGS1003 warns on `audit`). Reserve custom target names for genuinely distinct work with no canonical home (a `deploy` or `release`), not for fragmenting a canonical phase.

Custom target names must use the target-name charset: letters, digits, `-`, `_` (`types.ValidateTargetName`). `:`, `@`, and `/` are reserved for the grammar above.

### When does a name earn canonical status?

The eight names above are a closed, deliberate set, not a starting point. A new
name earns a place in it only if it passes all four:

1. **Universality** - the phase must mean something in every toolchain magus
   adapts. A phase that only makes sense for one language fails this test:
   `typecheck` is universal-sounding but Go and Rust type-check as part of
   `build`, not as a separate phase, so it does not earn a canonical slot.
2. **Distinctness** - it must be a genuine phase, not a subset of an existing
   one. `vet`, `audit`, `style`, and `typecheck` are all static analysis or
   formatting fragments of `lint`/`format` - or synonyms of an existing name
   (see [MGS1003](../reference/codes/magusfile/MGS1003.md)) - not phases of
   their own.
3. **Pipeline membership** - `ci` must need to order it against the other
   phases. A step nobody's `ci` ever sequences against `build`/`test`/`lint`
   has no claim on the canonical vocabulary.
4. **Tooling weight** - a canonical name can carry engine semantics beyond
   "a bucket of ops": `preflight`/`generate` get drift-gating (see
   [operations.md](operations.md)) precisely because they are canonical, not
   custom.

`security` is the worked example of a name that passed: every ecosystem magus
adapts has a mainstream scanner (universality), its advisory-database verdict
is time-dependent rather than a pure function of the tree - which is what
disqualifies it as a `lint` fragment and pairs it with `skip_cache`
(distinctness, tooling weight) - and a release pipeline must order it against
`build` and `test` (membership). It was carved out of `lint` for v1; earlier
docs composed scanners into `lint`, and that composition still works but is no
longer the guidance.

The v1 decision: this set is frozen at the eight above plus `ci`. `deploy`,
`release`, and `serve` stay custom by design - they are real, common phases,
but they are workspace-specific enough (which environment, which registry,
which port) that forcing one shape on them would be more prescriptive than
useful.

## Name normalization (casing & delimiters)

Target names are matched **case- and delimiter-insensitively**. magus normalizes
every name to canonical kebab-case (`types.Normalize`, a small
hand-rolled kebab-caser matching `samber/lo`'s `KebabCase` output without the
dependency) on **both** sides: when a magusfile _declares_ a target and when you
_reference_ one anywhere magus reads a target name. A target declared as
`go_build` is reachable by any spelling that normalizes to `go-build`:

```sh
magus run go-build      # kebab
magus run go_build      # snake
magus run goBuild       # camel
magus run GoBuild       # pascal
```

This is **normalize-both-sides**, not an alias table: there is exactly one
registered target (`go-build`), and the same normalizer runs over your input
before lookup, wherever that input enters.

The normalizer lowercases, inserts `-` at camelCase and letter/digit boundaries,
collapses each run of non-alphanumerics to a single `-`, and trims leading and
trailing `-`. What that means in practice, including the cases people trip over:

| You write     | magus resolves to | Rule                                        |
| ------------- | ----------------- | ------------------------------------------- |
| `go-build`    | `go-build`        | already canonical                           |
| `go_build`    | `go-build`        | `_` is a delimiter, not part of the name    |
| `goBuild`     | `go-build`        | camelCase boundary                          |
| `GoBuild`     | `go-build`        | PascalCase boundary                         |
| `HTTPServer`  | `http-server`     | an acronym run breaks before its last letter |
| `build2`      | `build-2`         | letter/digit boundary                       |
| `go--build`   | `go-build`        | delimiter runs collapse to one              |

The last three are the surprising ones. `HTTPServer` does not become
`h-t-t-p-server`, and `build2` gains a `-` you did not type, so a target declared
`build2` is referenced as `build-2` in anything that reports canonical names.

There is nothing proprietary here: the rule is ordinary kebab-case, and the Buzz
standard library's `strings\kebabCase` computes exactly what magus resolves with.
Run it and see:

<!-- magus-run -->
```buzz
import "std";
import "strings";

std\print(strings\kebabCase("go_build"));    // -> "go-build"
std\print(strings\kebabCase("goBuild"));     // -> "go-build"
std\print(strings\kebabCase("HTTPServer"));  // -> "http-server"
std\print(strings\kebabCase("build2"));      // -> "build-2"
```

That the two agree is not a coincidence you have to take on faith - a test holds
them to identical output on every case in the table above
([`TestKebabCaseMatchesNormalize`](https://github.com/egladman/magus/blob/main/std/strings_test.go)).
Internally the resolver calls `types.Normalize`, which is also reachable from a
magusfile as `magus\normalize` when you want to canonicalize a name yourself.

Buzz has testing built in, so the rule can be asserted rather than eyeballed - and
a `test` block is exactly how the magusfiles and spells in this repo are tested.
Save this and run `magus buzz -t names.buzz`:

```buzz title="names.buzz"
import "std";
import "strings";

test "every spelling of a name reaches one canonical form" {
    std\assert(strings\kebabCase("go_build") == "go-build");
    std\assert(strings\kebabCase("goBuild") == "go-build");
    std\assert(strings\kebabCase("GoBuild") == "go-build");
    std\assert(strings\kebabCase("go-build") == "go-build");
}

test "the two that surprise people" {
    std\assert(strings\kebabCase("HTTPServer") == "http-server");
    std\assert(strings\kebabCase("build2") == "build-2");
}
```

```text
ok    test "every spelling of a name reaches one canonical form"
ok    test "the two that surprise people"
---
2 passed, 0 failed, 0 skipped
```

Change one expected value and re-run to watch it fail - that is the whole testing
workflow, and it is the same `-t` flag the spells in this repo are tested with.

Two things about that snippet are deliberate. It has no Run button, because the
in-browser playground evaluates a script but does not execute `test` blocks - a
runnable version would sit there reporting nothing while a wrong assertion looked
like it passed. It also keeps to `strings\kebabCase`, which the standalone
runner can resolve without a workspace - and which is the point rather than a
concession: the rule really is just kebab-case.

Names are constrained to alphanumerics plus `-` and `_`. Everything else, `:` and
`@` especially, is reserved for reference grammar such as `spell::target`.

### The contract

- **Declare in any convention, call in any convention.** The declaration side
  (an `export fun` name in a magusfile) and the reference side (everywhere
  else) each run through the same normalizer, independently, before either is
  compared or stored.
- **Exactly one registered target.** Normalization is not a lookup table with
  multiple aliases resolving to one entry; there is one canonical key, and
  every spelling that normalizes to it reaches the same target.
- **Collisions are a hard load error.** Two declarations that collapse to the
  same canonical name (e.g. `fooBar` and `foo_bar`, both normalizing to
  `foo-bar`) make magus refuse the magusfile, naming the offending pair.
- **Convention drift is a `doctor` warning, not an error.** Mixed conventions
  across a workspace still resolve correctly, but `magus doctor` warns when it
  sees more than one naming convention, since call sites across CI YAML,
  scripts, and docs can drift out of sync with whichever one you typed.

### Where it applies

| Surface                                                 | Example                                                                                                    |
| ------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| Magusfile declarations (`export fun`)                   | `export fun go_build(...)` registers as `go-build`.                                                        |
| CLI `magus run` / `magus affected` arguments            | `magus run goBuild` reaches the target declared `go_build`.                                                |
| `magus\needs` target handles                            | `ctx.needs(goBuild)` resolves the target declared `go_build` (the handle's declared name is normalized). |
| The per-target policy map (`magus\project`'s `targets`) | A policy keyed `"goBuild"` applies to a target declared `go_build`, and vice versa.                        |
| Charm names                                             | `target:NoCache` and `target:no-cache` are the same charm.                                                 |
| Spell op keys                                           | A spell declaring an op `go_build` registers it as `go-build`.                                              |

One function does all of it: `types.Normalize`. There is no per-kind normalizer
and no alias table.

### Spell op keys are normalized too

<!-- magus-changed: v0.4.0 -->

Op keys are now normalized when the spell is decoded, the same as every other
name. Before, they were stored exactly as authored while every request arriving
at the spell had already been normalized - so an op declared `go_build` was
stored under `go_build`, looked up as `go-build`, and missed. The result was not
an error: the dispatcher treated it as "this spell does not provide that target"
and skipped it silently, logging only at debug level. The op was declared and
reachable by nothing.

Every built-in spell already wrote kebab-case op keys, so nothing about the
bundled spells changes. If you author a workspace-local spell with a `camelCase`
or `snake_case` op, it now works instead of silently never running.

### Where it deliberately does not apply

- **Spell names.** A spell's own name is matched byte-for-byte. A spell named
  `Go` and one named `go` are two different spells, not one; the registry will
  hold both.
- **Lookups by literal key.** Normalization canonicalizes what gets _stored_, not
  how a literal subscript is spelled. `typescript["tsc"]` is an ordinary map-key
  lookup into the value `import "magus/spell/typescript"` binds, so it must name the canonical
  (kebab) key. Likewise `golang::lint` is still a graceful no-op - the go spell's
  linter op is `golangci-lint`, and that is a different word, not a different
  casing. See [spell-qualified targets](#cli-extension-spell-qualified-targets).
- **Project paths.** `Path` is never normalized; `api` and `Api` are different
  (and, in practice, one of them just won't exist).

### Worked example

Given a magusfile declaring:

```buzz
export fun go_build(ctx: magus\Context, args: [str]) > void { golang["go-build"](ctx); }
```

all four of these resolve to the **one** registered target `go-build`, and thus
the **one** cache entry:

```sh
magus run go-build   # kebab: exact match
magus run go_build    # snake: normalizes to go-build
magus run goBuild     # camel: normalizes to go-build
magus run GoBuild     # pascal: normalizes to go-build
```

**Terminology note:** Target is magus's own term, distinct from Mage's vocabulary. In Mage, `extensions:build` is a single function name. In magus, `extensions` is a project Path and `build` is the target name: two orthogonal axes. Do not substitute Action, Operation, Task, Command, or Verb for Target in code, comments, or documentation.

## CLI extension: spell-qualified targets

On the command line only, a double-colon prefix invokes one spell's op **directly**, bypassing your composed targets. The token after `::` is a spell **op** (its CLI-command name), not a lifecycle target name:

```sh
magus run typescript::eslint api    # the eslint op of the typescript spell, in api/
magus run golang::go-vet                # the go-vet op of the go spell, all projects
magus run golang::golangci-lint-run         # the golangci-lint op of the go spell
```

This is an **escape hatch** for ad-hoc runs and introspection, not the everyday surface (compose ops into [targets](spells.md#spells-vs-targets) instead). Because it is op-direct, the name after `::` is matched against the spell's op keys verbatim (no kebab/case normalization, unlike target names; see [Naming operations](spells.md#naming-operations)):

- `golang::golangci-lint-run` runs that op.
- `golang::lint` is a graceful **no-op**: the go spell has no op named `lint` (its linter op is `golangci-lint`), so nothing runs.

The prefix is **not** stored in `Target`. The CLI strips it via `parseTarget` and passes it as a `WithSpellFilter` `RunOption`. The `ci` target does not support spell-qualified syntax.

## What is not part of a Target's identity

These modify execution but are **not** durable identity. Charms parse into `Target.Charms` but propagate via context; the rest travel as `RunOption` values alongside the target list.

| Input                    | Purpose                                                  |
| ------------------------ | -------------------------------------------------------- |
| `:charm,...`             | shared execution modifiers (see [charms.md](charms.md))  |
| `--shard` / `--n-shards` | CI matrix sharding (distributes projects across runners) |
| `--dry-run`              | prints what would run without executing                  |
| extra args after `--`    | forwarded to the underlying tool via `WithExtraArgs`     |

## Lifecycle: parse → expand → run

A target string goes through three stages before any tool is invoked:

```text
"web/studio:test"  (or: name token + positional projects)
      │
      ▼
ParseTarget(s)          → Target{Name:"test", Charms:[...]}
      │                   types/target.go
      ▼
Workspace.ExpandPath(t) → []Target (one concrete entry per matched project)
      │                   magus/select.go
      │
      │   (alternative: ExpandCwd resolves for the project under cwd)
      │   (alternative: ExpandAffected uses VCS diff to select projects,
      │                 and populates Target.Files)
      ▼
Magus.Run(ctx, targets) → executes each target, grouped by Name; charms
                          ride along on the context (WithCharms)
```

Key invariant: targets passed to `Run` should be concrete (each Path resolves to exactly one project). `ExpandPath`, `ExpandCwd`, and `ExpandAffected` enforce this.

## Glossary

| Term       | Definition                                                                                                                 |
| ---------- | -------------------------------------------------------------------------------------------------------------------------- |
| **Target** | An addressed unit of work: `Path + Name + Charms + Files`. The `Target` struct in `types/target.go`.                       |
| **Path**   | Project path relative to the workspace root. Empty or `/` means all projects.                                              |
| **Name**   | The target name: the operation to run. One of: `preflight`, `build`, `test`, `lint`, `format`, `security`, `clean`, `generate`. |
| **Charm**  | A shared execution modifier (e.g. `rw`). Carried in context; see [charms.md](charms.md).                                   |
| **Files**  | Repo-relative changed paths within a project. Populated by `ExpandAffected`; nil for explicit targets.                     |
| **Spell**  | A library of tool-native operations a target composes. Separate from Target; see [spells.md](spells.md).                   |
| **`ci`**   | An ordinary target you compose with `magus\needs`; `Magus.RunCI` only strips `rw`, anchors `magus affected`, and must-not-no-op. |

## See also

- [dependencies.md](dependencies.md): `magus\needs` versus `depends_on`, and how a cross-project `needs` folds into the affected set and the cache key.
- [dependencies.md, pattern forms](dependencies.md#pattern-forms): the `ctx.glob` grammar - suffix shorthand, globs, and `!` negation - with a worked example per form.
- [The guided tour, step 7](../tour/index.html#step-7): those pattern forms runnable in the browser.
- [operations.md](operations.md): the formal Operation definition and the work hierarchy (Spell → Operation → Target).
- [spells.md](spells.md): the operations a target composes, and [Spells vs Targets](spells.md#spells-vs-targets).
- [charms.md](charms.md): the execution modifiers attached after `:`.
- [engines.md](engines.md): the Buzz engine a magusfile runs on.
