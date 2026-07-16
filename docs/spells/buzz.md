---
title: buzz spell
description: "Buzz spell: check and test .buzz sources, plus run them through the magus interpreter."
tags: [buzz, spell, gopherbuzz, check, test, tools]
---

# buzz

The `buzz` spell checks and tests Buzz sources. Each op finds every `.buzz` file and runs `buzz --check`, `buzz --test`, or the magus interpreter over it.

**Runtime name:** `buzz` (source `spells/buzz/`)

**Version probe:** none

## Passing arguments to ops

Every op is invoked as `buzz["<op>"](opts?)`, where the optional options map accepts these keys - all optional, each appended to or shaping the forked command:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `buzz["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L108) |
| `cwd` | `str` | Working directory the command runs in. Defaults to the project directory. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L105) |
| `env` | `{str: str}` | Environment variables set for the process, on top of the inherited environment. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L112) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L120) |

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## buzz-check

check type-checks every Buzz source without running it (buzz --check). buzz takes one script per invocation, so find feeds xargs one file at a time (-n1; -print0 pairs with -0 for safe paths, and -r skips an empty set - the same -print0 | xargs pattern the bash spell uses for shellcheck.

**Command:** `sh -c find . -name '*.buzz' -print0 | xargs -0 -r -n1 buzz --check`

### Example

<!-- run-recorder -->
```buzz
// buzz-check parses every .buzz file: magus run check runs buzz --check over the tree.
import "magus";
import "magus/spell/buzz";

magus.project({ "spells": [buzz] });

export fun check(args: [str]) > void {
    buzz["buzz-check"]();
}
```

## buzz-test

test runs each source's Buzz `test {}` blocks (buzz --test).

**Command:** `sh -c find . -name '*.buzz' -print0 | xargs -0 -r -n1 buzz --test`

### Example

<!-- run-recorder -->
```buzz
// buzz-test runs the test blocks in every .buzz file via buzz --test.
import "magus";
import "magus/spell/buzz";

magus.project({ "spells": [buzz] });

export fun test(args: [str]) > void {
    buzz["buzz-test"]();
}
```

## magus-buzz

magus-buzz executes each source through `magus buzz`, magus's own embedded Buzz engine. It has no check-only mode - executing a file compiles, type-checks, and runs it - so this is the runtime sibling of `buzz-check`. It invokes "$MAGUS": magus exports MAGUS (the running binary's path, à la GNU Make's $(MAKE)) into every spell subprocess, so this resolves to the current magus even uninstalled / under `go run`, with no dependence on PATH. (A bare $MAGUS, not ${MAGUS:-magus}: Buzz reads {...} in a string as interpolation, and MAGUS is always set here anyway.)

**Command:** `sh -c find . -name '*.buzz' -print0 | xargs -0 -r -n1 "$MAGUS" buzz`

### Example

<!-- run-recorder -->
```buzz
// magus-buzz runs each .buzz file through the magus interpreter.
import "magus";
import "magus/spell/buzz";

magus.project({ "spells": [buzz] });

export fun run_buzz(args: [str]) > void {
    buzz["magus-buzz"]();
}
```

