---
title: fmt module
generated_from: reference/buzz/
aliases: [modules/fmt]
description: String formatting (printf-style).
tags: [fmt, module, stdlib, magusfile]
---

# fmt

String formatting (printf-style).

> **Naming convention:** import the module under its bare name (`import "fmt"`), reach members with a backslash, and call methods in `camelCase`: `fmt\someMethod`.

## Methods

### sprintf

Format string args into the template using Go printf verbs (e.g. %s, %q). Returns the formatted string.

**Signature:** `fmt\sprintf(format, args...) -> string` - [source](https://github.com/egladman/magus/blob/main/std/fmt.go#L33)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `format` | `string` |  | |
| `args` | `string` |  | |

**Returns:** string

