---
title: layout module
generated_from: reference/buzz/
aliases: [modules/layout]
description: Bindings for the Go package github.com/egladman/magus/libs/termkit/layout, generated from its exported API.
tags: [layout, module, stdlib, magusfile]
---

# layout

Bindings for the Go package github.com/egladman/magus/libs/termkit/layout, generated from its exported API. A value with state is an int handle and a record is a map. Writers are standard error and readers standard input.

> **Naming convention:** import the module under its bare name (`import "layout"`), reach members with a backslash, and call methods in `camelCase`: `layout\someMethod`.

## Methods

### cols

Cols is the display width of s in terminal columns.

**Signature:** `layout\cols(s) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `s`       | `string` |          |             |

**Returns:** int

### fit

Fit shortens text to width columns, ending in "..." when it cut.

**Signature:** `layout\fit(text, width) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `text`    | `string` |          |             |
| `width`   | `int`    |          |             |

**Returns:** string

### clip

Clip shortens s to cols columns without an ellipsis, never cutting inside an escape sequence and closing any style the cut left open.

**Signature:** `layout\clip(s, cols) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `s`       | `string` |          |             |
| `cols`    | `int`    |          |             |

**Returns:** string

### clipBytes

ClipBytes shortens plain text to nBytes bytes, ellipsis included, never splitting a rune.

**Signature:** `layout\clipBytes(s, nBytes) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `s`       | `string` |          |             |
| `nBytes`  | `int`    |          |             |

**Returns:** string

### columns

Columns starts the metadata of every row one column after the longest label, capped at [MaxLabel].

**Signature:** `layout\columns(rows, width, meta, selected) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

| Parameter  | Type     | Optional | Description |
| ---------- | -------- | -------- | ----------- |
| `rows`     | `any`    |          |             |
| `width`    | `int`    |          |             |
| `meta`     | `string` |          |             |
| `selected` | `string` |          |             |

**Returns:** any

### tree

Tree returns the branch drawn before item i of n siblings: a tee for every item but the last, which closes the branch.

**Signature:** `layout\tree(i, n, ascii) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

| Parameter | Type   | Optional | Description |
| --------- | ------ | -------- | ----------- |
| `i`       | `int`  |          |             |
| `n`       | `int`  |          |             |
| `ascii`   | `bool` |          |             |

**Returns:** string

### hints

Hints joins hints into one row no wider than width columns.

**Signature:** `layout\hints(hints, width, style) -> map[string]any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `hints`   | `any`    |          |             |
| `width`   | `int`    |          |             |
| `style`   | `string` |          |             |

**Returns:** map[string]any

### maxLabel

MaxLabel caps the column [Columns] aligns metadata to.

**Signature:** `layout\maxLabel() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

**Returns:** int

### separator

Separator joins metadata items and hints.

**Signature:** `layout\separator() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

**Returns:** string

