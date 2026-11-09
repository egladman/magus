---
title: pick module
generated_from: reference/buzz/
aliases: [modules/pick]
description: Bindings for the Go package github.com/egladman/magus/libs/termkit/pick, generated from its exported API.
tags: [pick, module, stdlib, magusfile]
---

# pick

Bindings for the Go package github.com/egladman/magus/libs/termkit/pick, generated from its exported API. A value with state is an int handle and a record is a map. Writers are standard error and readers standard input.

> **Naming convention:** import the module under its bare name (`import "pick"`), reach members with a backslash, and call methods in `camelCase`: `pick\someMethod`.

## Methods

### one

Pick blocks until the user selects an item or aborts.

**Signature:** `pick\one(p, items, opts) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/pick/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `p`       | `<invalid>` |          |             |
| `items`   | `<invalid>` |          |             |
| `opts`    | `<invalid>` |          |             |

**Returns:** <invalid>

### render

RenderPick returns the block a picker WOULD draw for the given state, without opening a terminal or running an input loop.

**Signature:** `pick\render(p, items, opts, filter, cursor) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/pick/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `p`       | `<invalid>` |          |             |
| `items`   | `<invalid>` |          |             |
| `opts`    | `<invalid>` |          |             |
| `filter`  | `<invalid>` |          |             |
| `cursor`  | `<invalid>` |          |             |

**Returns:** <invalid>

### errAborted

ErrAborted is returned when the user presses ESC, Ctrl-C, or Ctrl-D.

**Signature:** `pick\errAborted() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/pick/module.go)

**Returns:** <invalid>

