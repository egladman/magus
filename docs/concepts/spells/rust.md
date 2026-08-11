---
title: rust spell
description: "Rust toolchain spell: cargo build, test, clippy, fmt, and clean as magus ops."
tags: [rust, spell, cargo, build, test, tools]
---

# rust

The `rust` spell wires Cargo into a magusfile. Each op forks a `cargo` subcommand directly; `cargo-build` builds in release mode and `cargo-clippy` denies warnings, matching a CI-gating default.

**Runtime name:** `rust` (source `spells/rust/`)

**Version probe (rustc):** `rustc --version`

## Passing arguments to ops

Every op is invoked as `rust["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `rust["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L169) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L173) |


Working directory and environment are NOT options: they ride the context, as `rust["<op>"](ctx.withCwd("sub"))` and `rust["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise; passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## cargo-build

**Command:** `cargo build --release`

### Example

<!-- magus-run-recorder -->
```buzz
// cargo-build compiles in release mode: magus run build forks cargo build --release.
import "magus";
import "magus/spell/rust";

magus\project({ "spells": [rust] });

export fun build(ctx: magus\Context, args: [str]) > void {
    rust["cargo-build"](ctx);
}
```

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

**Command:** `cargo clippy -- -D warnings`

### Example

<!-- magus-run-recorder -->
```buzz
// cargo-clippy lints and denies warnings (-D warnings), gating CI on a clean run.
import "magus";
import "magus/spell/rust";

magus\project({ "spells": [rust] });

export fun clippy(ctx: magus\Context, args: [str]) > void {
    rust["cargo-clippy"](ctx);
}
```

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

## cargo-test

**Command:** `cargo test`

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

scip is the reserved op that runs the Rust SCIP indexer for the knowledge graph. magus injects MAGUS_SYMBOL_INDEX with the cache destination, so the index never lands in the tree; rust-analyzer's scip subcommand writes there via --output. The runner resolves the bare $MAGUS_SYMBOL_INDEX token against that destination, so no shell is needed to expand it.

**Command:** `rust-analyzer scip . --output $MAGUS_SYMBOL_INDEX`

