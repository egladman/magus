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

**Command:** `sh -c find . -name '*.buzz' -print0 | xargs -0 -r -n1 buzz --check`

## buzz-test

**Command:** `sh -c find . -name '*.buzz' -print0 | xargs -0 -r -n1 buzz --test`

## magus-buzz

**Command:** `sh -c find . -name '*.buzz' -print0 | xargs -0 -r -n1 "$MAGUS" buzz`

