---
title: fmt module
description: printf-style string formatting using Go verbs like %s and %q. One method, sprintf, that formats args into a template and returns the string.
tags: [fmt, sprintf, format, printf, string formatting, magus stdlib]
---

# fmt

String formatting (printf-style).

> **Naming convention:** import the module under its bare name (`import "fmt"`) and call methods in `camelCase` (`fmt.someMethod`).

## Methods

### sprintf

Format string args into the template using Go printf verbs (e.g. %s, %q). Returns the formatted string.

**Signature:** `fmt.sprintf(format, args...) → string` · [source](https://github.com/egladman/magus/blob/main/std/fmt.go#L32)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `format` | `string` |  | |
| `args` | `string` |  | |

**Returns:** string

