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

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `s`       | `<invalid>` |          |             |

**Returns:** <invalid>

### fit

Fit shortens text to width columns, ending in "..." when it cut.

**Signature:** `layout\fit(text, width) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `text`    | `<invalid>` |          |             |
| `width`   | `<invalid>` |          |             |

**Returns:** <invalid>

### clip

Clip shortens s to cols columns without an ellipsis, never cutting inside an escape sequence and closing any style the cut left open.

**Signature:** `layout\clip(s, cols) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `s`       | `<invalid>` |          |             |
| `cols`    | `<invalid>` |          |             |

**Returns:** <invalid>

### clipBytes

ClipBytes shortens plain text to nBytes bytes, ellipsis included, never splitting a rune.

**Signature:** `layout\clipBytes(s, nBytes) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `s`       | `<invalid>` |          |             |
| `nBytes`  | `<invalid>` |          |             |

**Returns:** <invalid>

### columns

Columns starts the metadata of every row one column after the longest label, capped at [MaxLabel].

**Signature:** `layout\columns(rows, width, meta, selected) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

| Parameter  | Type        | Optional | Description |
| ---------- | ----------- | -------- | ----------- |
| `rows`     | `<invalid>` |          |             |
| `width`    | `<invalid>` |          |             |
| `meta`     | `<invalid>` |          |             |
| `selected` | `<invalid>` |          |             |

**Returns:** <invalid>

### tree

Tree returns the branch drawn before item i of n siblings: a tee for every item but the last, which closes the branch.

**Signature:** `layout\tree(i, n, ascii) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `i`       | `<invalid>` |          |             |
| `n`       | `<invalid>` |          |             |
| `ascii`   | `<invalid>` |          |             |

**Returns:** <invalid>

### hints

Hints joins hints into one row no wider than width columns.

**Signature:** `layout\hints(hints, width, style) -> map[string]any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `hints`   | `<invalid>` |          |             |
| `width`   | `<invalid>` |          |             |
| `style`   | `<invalid>` |          |             |

**Returns:** <invalid>

### maxLabel

MaxLabel caps the column [Columns] aligns metadata to.

**Signature:** `layout\maxLabel() -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

**Returns:** <invalid>

### separator

Separator joins metadata items and hints.

**Signature:** `layout\separator() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/layout/module.go)

**Returns:** <invalid>

