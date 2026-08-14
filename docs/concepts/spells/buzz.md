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

Every op is invoked as `buzz["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `buzz["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L168) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L172) |


Working directory and environment are NOT options: they ride the context, as `buzz["<op>"](ctx.withCwd("sub"))` and `buzz["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise; passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## buzz-check

check type-checks every Buzz source without running it (buzz --check). buzz takes one script per invocation, so `sources` runs it once PER matched file (sourcesEach = true - the engine-side replacement for the `find | xargs -n1` this op used to shell out to; see Command.sources/sourcesEach in spells/op.go). A glob set that matches nothing runs buzz zero times rather than failing.

**Command:** `buzz --check`

### Example

<!-- magus-run-recorder -->
```buzz
// buzz-check parses every .buzz file: magus run check runs buzz --check over the tree.
import "magus";
import "magus/spell/buzz";

magus\project({ "spells": [buzz] });

export fun check(ctx: magus\Context, args: [str]) > void {
    buzz["buzz-check"](ctx);
}
```

## buzz-test

test runs each source's Buzz `test {}` blocks (buzz --test), one file per invocation - see buzzCheck above.

**Command:** `buzz --test`

### Example

<!-- magus-run-recorder -->
```buzz
// buzz-test runs the test blocks in every .buzz file via buzz --test.
import "magus";
import "magus/spell/buzz";

magus\project({ "spells": [buzz] });

export fun test(ctx: magus\Context, args: [str]) > void {
    buzz["buzz-test"](ctx);
}
```

## magus-buzz

magus-buzz executes each source through `magus buzz`, magus's own embedded Buzz engine, one file per invocation (see buzzCheck above). It has no check-only mode - executing a file compiles, type-checks, and runs it - so this is the runtime sibling of `buzz-check`. bin is the literal string "$MAGUS", not a shell variable reference: the runner resolves a bare $NAME token in Bin/Args against the values it controls for this invocation (see resolveRunnerRefs in internal/interp/bindings) - the same resolution that exports MAGUS (à la GNU Make's $(MAKE)) into every spell subprocess's environment - so this always runs the current magus, even uninstalled or under `go run`, with no dependence on PATH and no shell needed to expand it. (A bare "$MAGUS", not "${MAGUS:-magus}": Buzz reads {...} in a string as interpolation, and this string is never interpolated - it is matched literally.)

**Command:** `$MAGUS buzz`

### Example

<!-- magus-run-recorder -->
```buzz
// magus-buzz runs each .buzz file through the magus interpreter.
import "magus";
import "magus/spell/buzz";

magus\project({ "spells": [buzz] });

export fun run_buzz(ctx: magus\Context, args: [str]) > void {
    buzz["magus-buzz"](ctx);
}
```

