# Anatomy of a magus Spell

A **Spell** is a _library of tool-native operations_ for one toolchain, plus the cache and affected-set metadata that toolchain needs. The `go` spell exposes `go-build`/`go-test`/`go-vet`/`go-fmt`/`golangci-lint`/...; the `rust` spell exposes `cargo-build`/`cargo-test`/`cargo-clippy`/`cargo-fmt`/.... Each op is named after the CLI command it runs (see [Naming operations](#naming-operations)). A spell is **bound to a project** and **runs nothing on its own**: it contributes operations your magusfile composes into [targets](targets.md), and it tells the cache which files are inputs and outputs.

A spell is _how_ a tool does something (the `go-vet`, `cargo-clippy` ops); a target is _what_ you run (`magus run lint`). You **bind** spells and **invoke** targets. See [Spells vs Targets](#spells-vs-targets).

## Spells vs Targets

These are the two core nouns in magus, on orthogonal axes. Confusing them is the most common source of "why didn't my build do anything?".

|                         | **Spell**                                                                            | **Target**                                                                              |
| ----------------------- | ------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------- |
| **What it is**          | a library of tool-native operations (+ cache metadata)                               | an addressable unit of work you run                                                     |
| **Answers**             | _how_ a tool performs an operation                                                   | _what_ operation runs on _which_ project                                                |
| **Vocabulary**          | the tool's own CLI command (`go-vet`, `cargo-clippy`, `tsc`, `eslint`, `ruff-check`) | magus's lifecycle (`build`, `test`, `lint`, `format`, `clean`, `generate`, `preflight`) |
| **Who declares it**     | a built-in, a spell file (`spells/*.buzz`), or `magus.spell.define`                   | an exported function in your magusfile                                                  |
| **How it enters a run** | **bound** to a project via `magus.project.register`                                  | **invoked** via `magus run <name>`                                                      |
| **Runs on its own?**    | **No**: it only contributes ops + cache inputs                                       | **Yes**: it is the entry point                                                          |
| **Cardinality**         | many ops per spell; many spells per project                                          | one function per target name per project                                                |
| **Cache role**          | declares `needs`/`provides`/`claims` (the inputs/outputs)                            | the unit a cache key is computed and replayed for                                       |
| **Identity**            | a `name` + its ops                                                                   | `Path + Name` (see [targets.md](targets.md))                                            |

The relationship is **compositional**: a target's body calls spell ops.

```buzz
import "magus/spell/go";
magus.project.register(fun(p, cb) > bool { cb({ "spells": [go] }); return true; });   // bind the spell (runs nothing)

// targets are the runnable verbs; their bodies call the spell's ops. Op keys are
// the CLI command, so kebab names are reached by subscript (see Naming operations).
export fun build(_args: [str]) > void { go["go-build"]({ "cwd": "." }); }
export fun lint(_args: [str])  > void { go["golangci-lint"]({ "cwd": "." }); }
export fun test(_args: [str])  > void { go["go-test"]({ "cwd": "." }); }
```

```sh
magus run lint .         # runs your `lint` target, which calls go's golangci-lint op
```

### When to use which

- **Reach for a target** when you want a _runnable verb_: the thing a teammate or CI types (`magus run test api`). Targets are your **public surface**; declare one per lifecycle step you want runnable. Until you export a target for an operation, `magus run <op>` is a graceful no-op.
- **Reach for a spell** when you want to _package a toolchain's operations_ and _tell the cache which files matter_. Bind a built-in, load a spell file, or `magus.spell.define` an inline one. Call its ops from inside target bodies.
- **Skip the spell entirely** for a one-off step with arbitrary logic: write the target body directly with `extra.*` (e.g. `extra.os.exec(...)`). A spell earns its keep when an operation recurs and has cache inputs worth declaring.
- **Use the `::` escape hatch** (`magus run go::go-vet api`) only for ad-hoc runs or introspection. The everyday surface is your composed targets.

magus deliberately does **not** decide what "lint" or "format" means. A spell supplies tool-native operations in the tool's own words; your magusfile decides which op backs each lifecycle target. Toolchain knowledge lives in the spell (reusable, cacheable); policy lives in the magusfile (yours to compose).

## What a spell provides

A bound spell contributes three things to its project. Only operations are "runnable"; the other two are metadata that make caching and the affected set correct.

| Contribution   | Source                                  | Purpose                                                        |
| -------------- | --------------------------------------- | -------------------------------------------------------------- |
| **Operations** | `mgs_listTargets` (or `ops`)            | the tool-native actions a target can call                      |
| **`needs`**    | `mgs_listRequiredGlobs` (or `needs`)    | input globs hashed into the cache key; also affected-set seeds |
| **`provides`** | `mgs_listProvidedGlobs` (or `provides`) | output globs the cache snapshots and replays                   |
| **`claims`**   | `mgs_listClaimedGlobs` (or `claims`)    | files this spell owns for affected-set attribution             |

Binding a spell contributes its `needs`/`claims`/`provides` to that project's cache key and affected set even before you wire any target; it executes nothing until a target calls one of its ops.

### Operations come in two shapes

1. **Forked command (declarative).** The op is a `{cmd, args, charms}` record. magus forks the command directly (no shell, no variable expansion), so invocations are deterministic and injection-safe. This is the shape custom spells use most.

2. **Typed handler.** `mgs_listTargets` returns `{str: fun(Target, fun(any)) bool}`. A **fork** handler passes its command record to the injected `cb` callback; a **function-op** handler does host work in-VM (HTTP, signing, a cache backend's `get-entry`). The built-in spells use the handler form; read any [`spells/<name>/spell.buzz`](../spells) for a worked example.

Both shapes decode to the same thing, so a declarative record and a typed handler that declares the same command behave identically.

A function-op handler does host work in-VM (HTTP, signing, a cache backend's `get-entry`) instead of forking a single command, calling host modules (`extra.http`, `extra.crypto`) as needed. This is what lets a remote cache backend be authored as a spell. `magus doctor` enforces a doc comment on each function-op handler, captured by the Buzz parser at compile time.

The handler's second parameter is named **`cb`** (a plain callback): the handler calls `cb({cmd, args, charms})` once to declare the command it forks. The name stays `cb` rather than `op` on purpose — `op` already means two other things here (a charm patch's `op` field, and a spell's _ops_), so reusing it would be ambiguous.

## Binding a spell to a project

A spell only takes effect when its **handle** is passed to `magus.project.register`. Loading or defining a spell is pure; it registers nothing on its own.

### Built-in

Built-in spells are compiled into the magus binary.

```buzz
import "magus/spell/go";
magus.project.register(fun(p, cb) > bool { cb({ "spells": [go] }); return true; });
```

Available built-ins: `go`, `typescript`, `javascript`, `python`, `rust`, `zig`, `bash`, `docker`, `compose`, `kind`, `terraform`, `kustomize`, `json`, `yaml`, `toml`, `html`, `markdown`, `css`, `sql`.

### File spell

A workspace-local `spells/<name>.buzz` loaded by path:

```buzz
const rb = magus.spell.load("spells/ruby.buzz");
magus.project.register("gems/", fun(p, cb) > bool { cb({ "spells": [rb] }); return true; });
```

### Inline spell

For a spell used in only one magusfile, define it inline:

```buzz
const rb = magus.spell.define({
    "name": "ruby",
    "needs": fun(_dir: str) > [str] { return ["**/*.rb", "Gemfile.lock"]; },
    "provides": fun() > [str] { return ["vendor/bundle/**/*"]; },
    "ops": {
        "rspec":   { "cmd": "bundle", "args": ["exec", "rspec"] },
        "rubocop": { "cmd": "bundle", "args": ["exec", "rubocop", "--check"],
                     "charms": { "rw": {"ops": [{"op": "replace", "path": "/2", "value": "-A"}]} } },
    },
});
magus.project.register("gems/", fun(p, cb) > bool { cb({ "spells": [rb] }); return true; });
```

The handle exposes `handle.listTargets()` (sorted op names) for introspection.

## Composing spells

Spells do **not** import one another. There is no spell-to-spell `import`, and a built-in spell may import only the pure-types `magus/target` module (enforced by `SelfContainedBuiltinSource`). Composition happens one level up, at the **project**: bind several spells to the same project and let your targets call across them.

```buzz
import "magus/spell/go";
import "magus/spell/docker";
import "magus/spell/compose";
magus.project.register(fun(p, cb) > bool { cb({ "spells": [go, docker, compose] }); return true; });   // co-bound

export fun build(_args: [str]) > void {
    go["go-build"]({ "cwd": "." });
    docker.build({ "cwd": "." });        // one target, ops from two spells
}
```

The docker/compose relationship is exactly this **co-binding**, not an import: both are bound to a project and their ops are composed in target bodies. The cache sees the union of every bound spell's `needs`/`provides`/`claims`.

One magus API does take a spell handle as an argument: `magus.cache.remote(github)` wires a [function-op](#operations-come-in-two-shapes) spell (e.g. `actions`, `s3-cache`) as the remote cache backend. That is a magus call consuming a spell, not a spell importing a spell. For the shipped backends and how to set one up, see [Remote caching](remote-cache.md).

## Naming operations

An op's public name is the **map key** in `mgs_listTargets` (or the `ops` table). That key is what a magusfile calls and what `magus run spell::op` invokes. The implementation function is private. Name both after the CLI command, not after a magus lifecycle verb, so the spell is self-documenting and a developer who knows the toolchain can invoke an op without reading the magusfile.

**Op key: the CLI command, kebab-case, lowercase, no flags.** Write the command as you type it and replace spaces with hyphens. The golang spell:

| runs            | op key          | handler        |
| --------------- | --------------- | -------------- |
| `go build`      | `go-build`      | `goBuild`      |
| `go vet`        | `go-vet`        | `goVet`        |
| `go test`       | `go-test`       | `goTest`       |
| `gofmt -l`      | `go-fmt`        | `goFmt`        |
| `go mod tidy`   | `go-mod-tidy`   | `goModTidy`    |
| `golangci-lint` | `golangci-lint` | `golangCILint` |

Naming the op `golangci-lint` (not `lint`) and `go-fmt` (not `fmt`) says exactly which tool runs: there is no `go lint`, and `fmt` here is the `gofmt` binary. Multi-tool spells already work this way: typescript exposes `tsc`/`eslint`/`prettier`/`vitest`, one op per tool.

**Handler: the same command in lowerCamelCase, with Go-style initialisms** (`go-fmt` → `goFmt`, `golangci-lint` → `golangCILint`, `ruff-check` → `ruffCheck`). The handler name is invisible to magus for fork ops; it exists to tell the reader the exact binary.

**Not every op is a CLI command.** A no-op marker (typescript's `preflight`), a filesystem cleanup (zig's `clean`, which is `rm -rf`), or a cache-backend verb (github/s3 `get-entry`) is not a tool invocation, so keep a descriptive name.

**Op keys are matched verbatim** (no kebab/case normalization, unlike target names), so a kebab key is reached by subscript in a magusfile: `go["go-build"]()`, not `go.build()`. An op whose key is a valid identifier (`pytest`, `eslint`) can use dot: `py.pytest()`.

### Explicitness over magic (why `go::go-fmt` stutters)

The full-command convention is enforced even for streamlined toolchains like Go, where `go-build`/`go-test`/`go-fmt` all start with `go`. The result reads with a stutter on the CLI — `magus run go::go-fmt` — and that is **by design**:

- **Consistency across a polyglot repo.** Most languages split work across separate binaries (typescript: `tsc`/`eslint`/`prettier`/`vitest`; rust: `cargo`/`clippy`/`rustfmt`). A rule that says "name the op after the binary, always" is one rule for every spell, instead of a special abbreviation for the few single-binary toolchains.
- **No invented vocabulary.** `go-fmt` is `gofmt`; `golangci-lint` is `golangci-lint`. There is no magus-specific `lint`/`fmt` alias a reader has to learn or a magusfile has to map. The op name _is_ the command.
- **The stutter is the escape hatch's, not the everyday surface's.** You only type `go::go-fmt` for an ad-hoc op-direct run (see [targets.md](targets.md#cli-extension-spell-qualified-targets)). In normal use you compose `go["go-fmt"]()` into a `format` target and run `magus run format`. magus favors explicitness over magic: the spell says exactly what it runs, and policy (which op is your "format") lives in your magusfile.

## Authoring a custom spell

A spell file exposes the spell contract as `mgs_`-prefixed functions: the required `mgs_getName`, plus optional `mgs_listRequiredGlobs`, `mgs_listProvidedGlobs`, `mgs_listClaimedGlobs`, `mgs_getVersionCommand`, `mgs_isForeignProcess`, and `mgs_listTargets`.

Buzz (`spells/ruby.buzz`):

```buzz
export fun mgs_getName() > str { return "ruby"; }
export fun mgs_listRequiredGlobs(_dir: str) > [str] {
    return ["**/*.rb", "Gemfile", "Gemfile.lock", "*.gemspec", ".rubocop.yml"];
}
export fun mgs_listProvidedGlobs() > [str] { return ["vendor/bundle/**/*"]; }
export fun mgs_listTargets() > any {
    return {
        "bundle-install": { "cmd": "bundle", "args": ["install"] },
        "rspec":   { "cmd": "bundle", "args": ["exec", "rspec"] },
        "rubocop": { "cmd": "bundle", "args": ["exec", "rubocop", "--check"],
                     "charms": { "rw": {"ops": [{"op": "replace", "path": "/2", "value": "-A"}]} } },
    };
}
```

Then bind it and compose targets that call its ops:

```buzz
const rb = magus.spell.load("spells/ruby.buzz");
magus.project.register("gems/", fun(p, cb) > bool { cb({ "spells": [rb] }); return true; });

export fun test(_args: [str]) > void { rb.rspec({ "cwd": "gems/" }); }
export fun lint(_args: [str]) > void { rb.rubocop({ "cwd": "gems/" }); }
```

For cache-correctness rules (declare every input in `needs`, declare `provides` so outputs replay, toolchain-version footguns), see [Spells in the README](../README.md#custom-spells).

## Lifecycle: bind → contribute → compose → run

```
import / magus.spell.load / define     → a Spell handle (registers nothing)
      │
      ▼
register(fun(p, cb){ cb({spells}) })   → the spell is bound to the project;
      │                                   its needs/provides/claims now feed the
      │                                   project's cache key and affected set
      ▼
export fun <name>(...) > void {}        → a target whose body calls spell ops
      │                                   (spell.op({"cwd": ...})); this is the
      │                                   runnable verb
      ▼
magus run <name> <project>             → executes the target; spell ops fork
                                          their commands (cached by needs/provides)
```

Key invariant: **binding is not running.** A bound spell with no target wired is inert at run time but still shapes the cache key. A target with no spell behind it is just a function you wrote (valid; use `extra.*`).

## Glossary

| Term               | Definition                                                                                                                                                                                                                         |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Spell**          | A library of tool-native operations for one toolchain, plus its cache/affected metadata. Bound to a project; runs nothing on its own.                                                                                              |
| **Op (operation)** | One tool-native action a spell exposes, named after its CLI command (`go-vet`, `golangci-lint`). Reached by subscript on the handle (`go["go-vet"]()`) or via `spell::op` on the CLI. See [Naming operations](#naming-operations). |
| **Handle**         | The value returned by an `import`/`magus.spell.load`/`magus.spell.define`. Inert until passed to `magus.project.register`.                                                                                                         |
| **`needs`**        | Input globs (`mgs_listRequiredGlobs`). Hashed into the cache key; also seed the affected set.                                                                                                                                      |
| **`provides`**     | Output globs (`mgs_listProvidedGlobs`). What the cache snapshots and replays on a hit.                                                                                                                                             |
| **`claims`**       | Files a spell owns (`mgs_listClaimedGlobs`), for affected-set attribution.                                                                                                                                                         |
| **Fork op**        | An op that declares a `{cmd, args, charms}` command magus forks directly (no shell).                                                                                                                                               |
| **Function-op**    | An op whose handler does host work in-VM (e.g. a cache backend) rather than forking a single command.                                                                                                                             |
| **Target**         | The runnable unit a spell op is composed into. A separate concept; see [targets.md](targets.md).                                                                                                                                   |

## Worked examples in this repo

Read these spells under [`magus/spells/`](../spells) when you outgrow a plain fork spell:

| Spell                                           | Kind            | What it demonstrates                                                                                                                                                                        |
| ----------------------------------------------- | --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [`buf`](../spells/buf/spell.buzz)                | fork (built-in) | A **codegen producer**: `needs` (`.proto` + buf config) and `provides` (generated code), so editing a `.proto` reruns codegen and invalidates everything downstream of the generated files. |
| [`actions`](../spells/github/actions/spell.buzz) | function-op     | A **remote cache backend** over the GitHub Actions Cache API in pure Buzz: bearer auth, byte-level chunked upload/streamed download (`magus/extra/http`), wired with `magus.cache.remote`.  |
| [`s3-cache`](../spells/aws/s3-cache/spell.buzz)  | function-op     | A **remote cache backend** for S3/MinIO/R2/B2 that signs every request with **AWS SigV4** via `magus/extra/crypto`.                                                                         |

## See also

- [Anatomy of a magus Target](targets.md): the unit you _run_, and the CLI grammar for addressing it.
- [Charms](charms.md): execution modifiers that spell ops and targets both honor.
- [Engines](engines.md): how a magusfile runs on the embedded Buzz VM and the `mgs_` spell contract.
- [Spells (README)](../README.md#spells): built-ins list, extending a built-in, and custom-spell best practices.
- [`magus` module API](modules/magus.md): `magus.project.register`, `magus.spell.load`, `magus.spell.define`.
