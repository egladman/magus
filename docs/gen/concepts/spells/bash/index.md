---
title: bash spell
description: "Bash spell: shellcheck linting for shell scripts."
tags: [bash, spell, shell, shellcheck, lint, tools]
---

# bash

The `bash` spell lints shell scripts. Its single op finds every `.sh`/`.bash` file and runs `shellcheck` over the set.

**Runtime name:** `bash` (source `spells/bash/`)

**Version probe:** none

## Passing arguments to ops

Every op is invoked as `bash["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `bash["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L170) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L174) |


Working directory and environment are NOT options: they ride the context, as `bash["<op>"](ctx.withCwd("sub"))` and `bash["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise - passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## shellcheck

One shellcheck invocation over every shell source: find feeds xargs with NUL separators, and -r skips running shellcheck on an empty set. node_modules is pruned inside the find too, not just declared above: a declared ignore dir shapes what magus treats as sources, but this op's find is its own walk, so without the prune a single JS dependency shipping a .sh file fails the lint of a repo that owns none of it. TODO: this prune is a stopgap and duplicates knowledge the engine already has. An op handler is called ONCE with a null Target and reduced to static {cmd, args} (see recordOp in internal/spell/resolve.go), so it cannot expand a file list itself - the `sh -c find` is what defers the walk to execution time in the project dir. The fix is engine-side: let an op declare a sources placeholder that the runner expands per project from expandSources(..., IgnoreDirs), the same walk that builds cache keys. Then every spell inherits the declared ignore dirs instead of hardcoding names here.

**Command:** `sh -c find . -name node_modules -prune -o \( -name '*.sh' -o -name '*.bash' \) -print0 | xargs -0 -r shellcheck`

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

