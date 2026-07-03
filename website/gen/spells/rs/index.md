---
title: rs spell
description: "Rust toolchain spell: cargo build, test, clippy, fmt, and clean as magus ops."
tags: [rs, spell, rust, cargo, build, test, tools]
---

# rs

The `rs` spell wires Cargo into a magusfile. Each op forks a `cargo` subcommand directly; `cargo-build` builds in release mode and `cargo-clippy` denies warnings, matching a CI-gating default.

**Runtime name:** `rs` (source `spells/rust/`)

**Version probe:** `rustc --version`

## Passing arguments to ops

Every op is invoked as `rs["<op>"](opts?)`, where the optional options map accepts these keys - all optional, each appended to or shaping the forked command:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `rs["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L107) |
| `cwd` | `str` | Working directory the command runs in. Defaults to the project directory. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L104) |
| `env` | `{str: str}` | Environment variables set for the process, on top of the inherited environment. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L111) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L119) |

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## cargo-build

**Command:** `cargo build --release`

### Example

<!-- run-recorder -->
```buzz
// cargo-build compiles in release mode: magus run build forks cargo build --release.
import "magus";
import "magus/spell/rs";

magus.project({ "spells": [rs] });

export fun build(args: [str]) > void {
    rs["cargo-build"]();
}
```

## cargo-clean

**Command:** `cargo clean`

### Example

<!-- run-recorder -->
```buzz
// Wire cargo-clean into a clean target: magus run clean forks cargo clean.
import "magus";
import "magus/spell/rs";

magus.project({ "spells": [rs] });

export fun clean(args: [str]) > void {
    rs["cargo-clean"]();
}
```

## cargo-clippy

**Command:** `cargo clippy -- -D warnings`

### Example

<!-- run-recorder -->
```buzz
// cargo-clippy lints and denies warnings (-D warnings), gating CI on a clean run.
import "magus";
import "magus/spell/rs";

magus.project({ "spells": [rs] });

export fun clippy(args: [str]) > void {
    rs["cargo-clippy"]();
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

<!-- run-recorder -->
```buzz
// cargo-fmt checks formatting; the rw charm (magus run format:rw) rewrites in place.
import "magus";
import "magus/spell/rs";

magus.project({ "spells": [rs] });

export fun format(args: [str]) > void {
    rs["cargo-fmt"]();
}
```

## cargo-test

**Command:** `cargo test`

### Example

<!-- run-recorder -->
```buzz
// Wire cargo-test into a test target: magus run test forks cargo test.
import "magus";
import "magus/spell/rs";

magus.project({ "spells": [rs] });

export fun test(args: [str]) > void {
    rs["cargo-test"]();
}
```

