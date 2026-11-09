---
title: notify module
generated_from: reference/buzz/
aliases: [modules/notify]
description: Bindings for the Go package github.com/egladman/magus/libs/termkit/notify, generated from its exported API.
tags: [notify, module, stdlib, magusfile]
---

# notify

Bindings for the Go package github.com/egladman/magus/libs/termkit/notify, generated from its exported API. A value with state is an int handle and a record is a map. Writers are standard error and readers standard input.

> **Naming convention:** import the module under its bare name (`import "notify"`), reach members with a backslash, and call methods in `camelCase`: `notify\someMethod`.

## Methods

### new

New returns a notifier that will draw into the bottom of z, holding at most max notifications at once.

**Signature:** `notify\new(z, max) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/notify/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `z`       | `<invalid>` |          |             |
| `max`     | `<invalid>` |          |             |

**Returns:** <invalid>

### stderr

Stderr returns the process-wide notification band on standard error, creating it on first use.

**Signature:** `notify\stderr() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/notify/module.go)

**Returns:** <invalid>

### releaseStderr

ReleaseStderr stops the process's notification band and hands back every leased row on standard error.

**Signature:** `notify\releaseStderr()` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/notify/module.go)

### stackNotify

Notify pushes a notification onto the stack, to be shown for ttl.

**Signature:** `notify\stackNotify(stack, text, style, ttl)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/notify/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `stack`   | `<invalid>` |          |             |
| `text`    | `<invalid>` |          |             |
| `style`   | `<invalid>` |          |             |
| `ttl`     | `<invalid>` |          |             |

### stackPin

Pin raises a notification about a CONDITION that is currently true, and keeps it until [Stack.Clear] retracts it.

**Signature:** `notify\stackPin(stack, key, text, style)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/notify/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `stack`   | `<invalid>` |          |             |
| `key`     | `<invalid>` |          |             |
| `text`    | `<invalid>` |          |             |
| `style`   | `<invalid>` |          |             |

### stackClear

Clear retracts the pinned notification named by key, if it is showing.

**Signature:** `notify\stackClear(stack, key)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/notify/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `stack`   | `<invalid>` |          |             |
| `key`     | `<invalid>` |          |             |

### stackClose

Close stops the sweeper and gives the band back.

**Signature:** `notify\stackClose(stack)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/notify/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `stack`   | `<invalid>` |          |             |

