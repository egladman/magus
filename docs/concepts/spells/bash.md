---
title: bash spell
description: "Bash spell: shellcheck linting, shfmt formatting, and bats tests for shell scripts."
tags: [bash, spell, shell, shellcheck, lint, tools]
---

# bash

The `bash` spell covers the shell lifecycle: `shellcheck` lints every `.sh`/`.bash` file, `shfmt` formats them (diff by default, rewrite under `rw`), and `bats` runs a test suite. Extensionless shebang scripts are a known limit - list them explicitly via op args.

**Runtime name:** `bash` (source `spells/bash/`)

**Version probe:** none

**Named probes:** `bats` (`bats --version`), `shellcheck` (`shellcheck --version`), `shfmt` (`shfmt --version`) - each records UNPROBED when the tool is absent, and moves the cache key when installed.

## Passing arguments to ops

Every op is invoked as `bash["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `bash["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L233) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L237) |


Working directory and environment are NOT options: they ride the context, as `bash["<op>"](ctx.withCwd("sub"))` and `bash["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise - passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## bats

bats runs Bash Automated Testing System suites - the mainstream shell test framework. The test directory is the caller's fact ({"args": ["test/"]}); bats errors clearly without one.

**Command:** `bats`

## shellcheck

One shellcheck invocation over every shell source: find feeds xargs with NUL separators, and -r skips running shellcheck on an empty set. node_modules and .claude/worktrees are pruned inside the find too, not just declared above: a declared ignore dir shapes what magus treats as sources, but this op's find is its own walk. Without the prunes, third-party shell files or stale agent worktrees make the current project fail lint for code it does not own.

**Command:** `sh -c find . \( -name node_modules -o -path './.claude/worktrees' \) -prune -o \( -name '*.sh' -o -name '*.bash' \) -print0 | xargs -0 -r shellcheck`

### Example

<!-- magus-run-recorder -->
```buzz
// shellcheck lints every .sh/.bash script found under the project.
import "magus";
import "magus/spell/bash";

magus\project({ "spells": [bash] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    bash["shellcheck"](ctx);
}
```

## shfmt

shfmt is the shell FORMATTER, giving `format` a meaning for shell projects (shellcheck alone left the lint/format divide one-sided: rw had nothing to write). Base -d prints a diff and exits non-zero on drift; the rw charm swaps it for -w to rewrite in place. shfmt walks directories itself and skips non-shell files, so it takes `.` rather than a find pipeline - which also keeps its argv reachable by charms (a sh -c string is not).

**Command:** `shfmt -d .`

### rw

Replaces `-d` with `-w`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "replace",
    "path": "/0",
    "value": "-w"
  }
]
```

</details>

### Example

<!-- magus-run-recorder -->
```buzz
// shfmt is the shell formatter, composing into the canonical `format` target
// the way gofmt does for Go: `magus run format` diffs (-d, non-zero on drift),
// and `magus run format:rw` rewrites in place.
import "magus";
import "magus/spell/bash";

magus\project({ "spells": [bash] });

export fun format(ctx: magus\Context, args: [str]) > void {
    bash["shfmt"](ctx);
}
```

