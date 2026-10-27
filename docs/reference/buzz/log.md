---
title: log module
generated_from: reference/buzz/
aliases: [modules/log]
description: "Emit a message at a level through magus's own logger. Message text uses Buzz `{expr}` interpolation; structured fields go in attrs. Logger is the injectable protocol: import \"log\" as _, then object<Logger>."
tags: [log, module, stdlib, magusfile]
---

# log

Emit a message at a level through magus's own logger, so it honors -q/-v/-vv, renders in the run's format, is redacted, and is captured in the run log. Unlike std\print, which is an uncontrolled bare line. Message text uses Buzz `{expr}` interpolation; pass structured fields as `attrs` so they stay queryable in the run log rather than baked into the string. `Logger` is the protocol for an injectable logger (same methods as the free functions): `import "log" as _` then `object<Logger>` to swap Std for Quiet without rewriting call sites.

> **Naming convention:** import the module under its bare name (`import "log"`), reach members with a backslash, and call methods in `camelCase`: `log\someMethod`.

## Methods

### trace

Log at trace level (shown at -vvv). For detail worth having when reconstructing a run and noise at any other time.

**Signature:** `log\trace(message, [attrs])` - [source](https://github.com/egladman/magus/blob/main/std/log.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `message` | `string`         |          |             |
| `attrs`   | `map[string]any` | yes      |             |

### debug

Log at debug level (shown at -v).

**Signature:** `log\debug(message, [attrs])` - [source](https://github.com/egladman/magus/blob/main/std/log.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `message` | `string`         |          |             |
| `attrs`   | `map[string]any` | yes      |             |

### info

Log at info level (shown by default, hidden by -q). Prefer `log\info("pulled {ref}", attrs: {"digest": d})` over stuffing fields into the message.

**Signature:** `log\info(message, [attrs])` - [source](https://github.com/egladman/magus/blob/main/std/log.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `message` | `string`         |          |             |
| `attrs`   | `map[string]any` | yes      |             |

### warn

Log at warn level: something the reader should act on eventually.

**Signature:** `log\warn(message, [attrs])` - [source](https://github.com/egladman/magus/blob/main/std/log.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `message` | `string`         |          |             |
| `attrs`   | `map[string]any` | yes      |             |

### error

Log at error level. This RECORDS a problem; it does not fail the target - raise (or os\exit) is what ends a run.

**Signature:** `log\error(message, [attrs])` - [source](https://github.com/egladman/magus/blob/main/std/log.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `message` | `string`         |          |             |
| `attrs`   | `map[string]any` | yes      |             |

### at

Log at a level chosen at runtime. Use it when the level is DATA rather than a literal - mapping a scanner's severity onto magus's levels, say - and the five named methods when it is not.

**Signature:** `log\at(level, message, [attrs])` - [source](https://github.com/egladman/magus/blob/main/std/log.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `level`   | `string`         |          |             |
| `message` | `string`         |          |             |
| `attrs`   | `map[string]any` | yes      |             |

