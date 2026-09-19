---
title: flags module
generated_from: reference/buzz/
aliases: [modules/flags]
description: "Parse a script's argv against the flags it declares."
tags: [flags, module, stdlib, magusfile]
---

# flags

Parse a script's argv against the flags it declares.

> **Naming convention:** import the module under its bare name (`import "flags"`), reach members with a backslash, and call methods in `camelCase`: `flags\someMethod`.

## Methods

### parse

Parse argv against the declared flags, returning {values, positionals, unknown}: switches take no value and record "true", valued flags take the next word or an =value suffix, everything after `--` is a positional, and every argument that was not declared is returned in unknown rather than guessed at. Errors when a valued flag is given no value.

**Signature:** `flags\parse(argv, switches, valued) -> FlagParse` - [source](https://github.com/egladman/magus/blob/main/std/flags.go#L55)

| Parameter  | Type       | Optional | Description |
| ---------- | ---------- | -------- | ----------- |
| `argv`     | `[]string` |          |             |
| `switches` | `[]string` |          |             |
| `valued`   | `[]string` |          |             |

**Returns:** map[string]any

