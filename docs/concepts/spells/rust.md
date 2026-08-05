---
title: rust spell
aliases: [concepts/spells/rs]
description: "Rust toolchain spell: cargo build, check, test, clippy, fmt, run, doc, bench, audit, and deny as magus ops."
tags: [rust, spell, cargo, build, test, tools]
---

# rust

The `rust` spell wires Cargo into a magusfile. Each op forks a `cargo` subcommand directly with no baked-in flags: the build profile (`--release`) and lint policy (`-- -D warnings`) are the magusfile's to append through the op's args option.

**Runtime name:** `rust` (source `spells/rust/`)

**Version probe:** `rustc --version`

**Named probes:** `cargo-audit` (`cargo-audit --version`), `cargo-deny-check` (`cargo-deny --version`) - each records UNPROBED when the tool is absent, and moves the cache key when installed.

## Passing arguments to ops

Every op is invoked as `rust["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `rust["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L233) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L237) |


Working directory and environment are NOT options: they ride the context, as `rust["<op>"](ctx.withCwd("sub"))` and `rust["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise - passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## cargo-audit

cargo-audit checks Cargo.lock against the RustSec advisory database; cargo-deny is the broader policy gate (advisories plus licenses/bans/sources, per its deny.toml). Both are mainstream - the magusfile picks one (or both) to compose into the canonical `security` target. Advisory verdicts track the database, not the tree, so pair that target with skip_cache (see targets.md).

**Command:** `cargo audit`

### Example

<!-- magus-run-recorder -->
```buzz
// cargo-audit checks Cargo.lock against the RustSec advisory database, so it
// composes into the canonical `security` target. The verdict tracks the
// advisory database, not the tree - declare skip_cache on the target so a
// replayed pass cannot hide a newly published CVE.
import "magus";
import "magus/spell/rust";

magus\project({ "spells": [rust], "targets": {
    "security": {"skip_cache": "reads the RustSec advisory database, which changes independently of this tree"},
} });

export fun security(ctx: magus\Context, args: [str]) > void {
    rust["cargo-audit"](ctx);
}
```

## cargo-bench

**Command:** `cargo bench`

## cargo-build

Plain `cargo build`, no --release: profile is the magusfile's call, made by appending `--release` (or `--profile <name>`) through the op's args option. Baking a profile in here was an opinion the caller could not remove - op args only append.

**Command:** `cargo build`

### debug

Appends `--verbose`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/-",
    "value": "--verbose"
  }
]
```

</details>

### Example

<!-- magus-run-recorder -->
```buzz
// cargo-build compiles the crate: magus run build forks cargo build. The profile
// is the magusfile's call - append {"args": ["--release"]} to build for release.
import "magus";
import "magus/spell/rust";

magus\project({ "spells": [rust] });

export fun build(ctx: magus\Context, args: [str]) > void {
    rust["cargo-build"](ctx);
}
```

## cargo-check

cargo-check is the fast typecheck (no codegen) - the dev-loop command - and the natural lint sibling to clippy for projects that want the cheap gate.

**Command:** `cargo check`

### debug

Appends `--verbose`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/-",
    "value": "--verbose"
  }
]
```

</details>

## cargo-clean

**Command:** `cargo clean`

### Example

<!-- magus-run-recorder -->
```buzz
// Wire cargo-clean into a clean target: magus run clean forks cargo clean.
import "magus";
import "magus/spell/rust";

magus\project({ "spells": [rust] });

export fun clean(ctx: magus\Context, args: [str]) > void {
    rust["cargo-clean"](ctx);
}
```

## cargo-clippy

Plain `cargo clippy`, no `-- -D warnings`: deny-by-default is a project policy, not this spell's - a magusfile that wants it appends the args. The rw charm needs --allow-dirty/--allow-staged alongside --fix because clippy refuses to rewrite a tree with uncommitted changes, which is exactly the state a local `lint:rw` runs in. No gha charm: clippy has no GitHub-native output format (only --message-format=json, which needs post-processing).

**Command:** `cargo clippy`

### debug

Appends `--verbose`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/-",
    "value": "--verbose"
  }
]
```

</details>

### rw

Inserts `--fix`, inserts `--allow-dirty`, inserts `--allow-staged`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/1",
    "value": "--fix"
  },
  {
    "op": "add",
    "path": "/2",
    "value": "--allow-dirty"
  },
  {
    "op": "add",
    "path": "/3",
    "value": "--allow-staged"
  }
]
```

</details>

### Example

<!-- magus-run-recorder -->
```buzz
// cargo-clippy is static analysis, so it composes into the canonical `lint`
// target. Denying warnings is a project policy the magusfile opts into by
// appending the args, as here; the op itself bakes no policy in.
import "magus";
import "magus/spell/rust";

magus\project({ "spells": [rust] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    rust["cargo-clippy"](ctx, {"args": ["--", "-D", "warnings"]});
}
```

## cargo-deny-check

**Command:** `cargo deny check`

### Example

<!-- magus-run-recorder -->
```buzz
// cargo-deny is the broader policy gate: advisories plus licenses, bans, and
// sources, per the project's deny.toml. A workspace picks cargo-audit,
// cargo-deny, or both for its `security` target; the advisory half tracks the
// database, not the tree, so skip_cache applies the same way.
import "magus";
import "magus/spell/rust";

magus\project({ "spells": [rust], "targets": {
    "security": {"skip_cache": "reads the RustSec advisory database, which changes independently of this tree"},
} });

export fun security(ctx: magus\Context, args: [str]) > void {
    rust["cargo-deny-check"](ctx);
}
```

## cargo-doc

Plain `cargo doc`: --no-deps is the common choice but a choice all the same, appended by the magusfile, not baked in here.

**Command:** `cargo doc`

## cargo-fetch

cargo-fetch pre-downloads the locked dependency graph - the real op behind a preflight target, and the way a network-restricted build warms its cache.

**Command:** `cargo fetch`

## cargo-fmt

**Command:** `cargo fmt -- --check`

### rw

Drops `--check`, drops `--`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "remove",
    "path": "/2"
  },
  {
    "op": "remove",
    "path": "/1"
  }
]
```

</details>

### Example

<!-- magus-run-recorder -->
```buzz
// cargo-fmt checks formatting; the rw charm (magus run format:rw) rewrites in place.
import "magus";
import "magus/spell/rust";

magus\project({ "spells": [rust] });

export fun format(ctx: magus\Context, args: [str]) > void {
    rust["cargo-fmt"](ctx);
}
```

## cargo-run

**Command:** `cargo run`

## cargo-test

**Command:** `cargo test`

### debug

Appends `--verbose`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/-",
    "value": "--verbose"
  }
]
```

</details>

### Example

<!-- magus-run-recorder -->
```buzz
// Wire cargo-test into a test target: magus run test forks cargo test.
import "magus";
import "magus/spell/rust";

magus\project({ "spells": [rust] });

export fun test(ctx: magus\Context, args: [str]) > void {
    rust["cargo-test"](ctx);
}
```

## scip

scip is the reserved op that runs the Rust SCIP indexer for the knowledge graph. magus injects MAGUS_SYMBOL_INDEX with the cache destination, so the index never lands in the tree; rust-analyzer's scip subcommand writes there via --output. Run through sh so the env var expands.

**Command:** `sh -c rust-analyzer scip . --output "$MAGUS_SYMBOL_INDEX"`

