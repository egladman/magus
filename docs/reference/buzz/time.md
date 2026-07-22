---
title: time module
aliases: [modules/time]
description: Timestamp formatting/parsing and duration parsing (Go time, UTC).
tags: [time, module, stdlib, magusfile]
---

# time

Timestamp formatting/parsing and duration parsing (Go time, UTC).

> **Naming convention:** import the module under its bare name (`import "time"`) and call methods in `camelCase` (`time.someMethod`).

## Methods

### format

Render Unix-millis as a string using a Go reference layout (UTC).

**Signature:** `time.format(layout, unix_millis) → string` · [source](https://github.com/egladman/magus/blob/main/std/time.go#L71)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `layout` | `string` |  | |
| `unix_millis` | `float64` |  | |

**Returns:** string

### parse

Parse a string with a Go reference layout into Unix-millis (UTC); errors on mismatch.

**Signature:** `time.parse(layout, value) → float64` · [source](https://github.com/egladman/magus/blob/main/std/time.go#L86)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `layout` | `string` |  | |
| `value` | `string` |  | |

**Returns:** float64

### parse_duration

Parse a Go duration string (e.g. "168h", "1h30m") into milliseconds; errors on mismatch.

**Signature:** `time.parseDuration(duration) → float64` · [source](https://github.com/egladman/magus/blob/main/std/time.go#L95)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `duration` | `string` |  | |

**Returns:** float64

### now_iso

Return the current UTC time as an RFC 3339 string. For the raw epoch-millis value use Buzz's os.time().

**Signature:** `time.nowIso() → string` · [source](https://github.com/egladman/magus/blob/main/std/time.go#L79)

**Returns:** string

### add

Add a Go duration string (e.g. "24h", "-1h30m") to a Unix-millis timestamp; returns the new Unix-millis timestamp.

**Signature:** `time.add(unix_millis, duration) → float64` · [source](https://github.com/egladman/magus/blob/main/std/time.go#L105)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `unix_millis` | `float64` |  | |
| `duration` | `string` |  | |

**Returns:** float64

### diff

Return a minus b in milliseconds (positive when a is later than b).

**Signature:** `time.diff(a, b) → float64` · [source](https://github.com/egladman/magus/blob/main/std/time.go#L115)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `a` | `float64` |  | |
| `b` | `float64` |  | |

**Returns:** float64

