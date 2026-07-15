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

Every op is invoked as `bash["<op>"](opts?)`, where the optional options map accepts these keys - all optional, each appended to or shaping the forked command:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `bash["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L108) |
| `cwd` | `str` | Working directory the command runs in. Defaults to the project directory. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L105) |
| `env` | `{str: str}` | Environment variables set for the process, on top of the inherited environment. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L112) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L120) |

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## shellcheck

One shellcheck invocation over every shell source: find feeds xargs with NUL separators, and -r skips running shellcheck on an empty set.

**Command:** `sh -c find . \( -name '*.sh' -o -name '*.bash' \) -print0 | xargs -0 -r shellcheck`

### Example

<!-- run-recorder -->
```buzz
// shellcheck lints every .sh/.bash script found under the project.
import "magus";
import "magus/spell/bash";

magus.project({ "spells": [bash] });

export fun lint(args: [str]) > void {
    bash["shellcheck"]();
}
```

