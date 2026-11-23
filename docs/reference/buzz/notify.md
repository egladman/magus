---
title: notify module
generated_from: reference/buzz/
aliases: [modules/notify]
description: Bindings for the Go package github.com/egladman/magus/libs/termkit/notify, generated from its exported API.
tags: [notify, module, stdlib, magusfile]
---

# notify

Bindings for the Go package github.com/egladman/magus/libs/termkit/notify, generated from its exported API. A value with state is an int handle. Record methods belong to typed objects. Writers are standard error and readers standard input.

> **Naming convention:** import the module under its bare name (`import "notify"`), reach members with a backslash, and call methods in `camelCase`: `notify\someMethod`.

## Methods

### new

New returns a notifier that will draw into the bottom of z, holding at most max notifications at once.

**Signature:** `notify\new(z, max) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/notify/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `z`       | `int` |          |             |
| `max`     | `int` |          |             |

**Returns:** int

### stderr

Stderr returns the process-wide notification band on standard error, creating it on first use.

**Signature:** `notify\stderr() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/notify/module.go)

**Returns:** int

### releaseStderr

ReleaseStderr stops the process's notification band and hands back every leased row on standard error.

**Signature:** `notify\releaseStderr() -> result\Result::<bool>` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/notify/module.go)

**Returns:** map[string]any

### stackNotify

Notify pushes a notification onto the stack, to be shown for ttl.

**Signature:** `notify\stackNotify(stack, text, style, ttl) -> result\Result::<bool>` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/notify/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `stack`   | `int`    |          |             |
| `text`    | `string` |          |             |
| `style`   | `string` |          |             |
| `ttl`     | `int`    |          |             |

**Returns:** map[string]any

### stackPin

Pin raises a notification about a CONDITION that is currently true, and keeps it until [Stack.Clear] retracts it.

**Signature:** `notify\stackPin(stack, key, text, style) -> result\Result::<bool>` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/notify/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `stack`   | `int`    |          |             |
| `key`     | `string` |          |             |
| `text`    | `string` |          |             |
| `style`   | `string` |          |             |

**Returns:** map[string]any

### stackClear

Clear retracts the pinned notification named by key, if it is showing.

**Signature:** `notify\stackClear(stack, key) -> result\Result::<bool>` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/notify/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `stack`   | `int`    |          |             |
| `key`     | `string` |          |             |

**Returns:** map[string]any

### stackClose

Close stops the sweeper and gives the band back.

**Signature:** `notify\stackClose(stack) -> result\Result::<bool>` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/notify/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `stack`   | `int` |          |             |

**Returns:** map[string]any

