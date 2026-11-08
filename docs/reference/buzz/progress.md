---
title: progress module
generated_from: reference/buzz/
aliases: [modules/progress]
description: Bindings for the Go package github.com/egladman/magus/libs/termkit/progress, generated from its exported API.
tags: [progress, module, stdlib, magusfile]
---

# progress

Bindings for the Go package github.com/egladman/magus/libs/termkit/progress, generated from its exported API. A value with state is an int handle and a record is a map. Writers are standard error and readers standard input.

> **Naming convention:** import the module under its bare name (`import "progress"`), reach members with a backslash, and call methods in `camelCase`: `progress\someMethod`.

## Methods

### bar

Bar draws done of total in cols cells, filled in eighths so it moves a fraction of a cell at a time.

**Signature:** `progress\bar(done, total, cols, track) -> map[string]any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `done`    | `int`    |          |             |
| `total`   | `int`    |          |             |
| `cols`    | `int`    |          |             |
| `track`   | `string` |          |             |

**Returns:** map[string]any

### loaderWidth

Width returns l sized to cols columns.

**Signature:** `progress\loaderWidth(loader, cols) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `loader`  | `int` |          |             |
| `cols`    | `int` |          |             |

**Returns:** int

### loaderHeight

Height returns l sized to rows rows.

**Signature:** `progress\loaderHeight(loader, rows) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `loader`  | `int` |          |             |
| `rows`    | `int` |          |             |

**Returns:** int

### loaderLabel

Label returns l animating text.

**Signature:** `progress\loaderLabel(loader, text) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `loader`  | `int`    |          |             |
| `text`    | `string` |          |             |

**Returns:** int

### loaderFrame

Frame draws the frame elapsed after the animation started, one Line per row.

**Signature:** `progress\loaderFrame(loader, elapsed) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `loader`  | `int` |          |             |
| `elapsed` | `int` |          |             |

**Returns:** any

### names

Names lists every loader in catalog order.

**Signature:** `progress\names() -> []string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** []string

### lookup

Lookup returns the loader called name, such as "glyph/dots".

**Signature:** `progress\lookup(name) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `name`    | `string` |          |             |

**Returns:** int

### loaderName

The Name field of a Loader.

**Signature:** `progress\loaderName(loader) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `loader`  | `int` |          |             |

**Returns:** string

### loaderKind

The Kind field of a Loader.

**Signature:** `progress\loaderKind(loader) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `loader`  | `int` |          |             |

**Returns:** string

### loaderInterval

The Interval field of a Loader.

**Signature:** `progress\loaderInterval(loader) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `loader`  | `int` |          |             |

**Returns:** int

### loaderCols

The Cols field of a Loader.

**Signature:** `progress\loaderCols(loader) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `loader`  | `int` |          |             |

**Returns:** int

### loaderRows

The Rows field of a Loader.

**Signature:** `progress\loaderRows(loader) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `loader`  | `int` |          |             |

**Returns:** int

### glyph

The footprints.

**Signature:** `progress\glyph() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### rotation

The footprints.

**Signature:** `progress\rotation() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### strip

The footprints.

**Signature:** `progress\strip() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### text

The footprints.

**Signature:** `progress\text() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### span

The footprints.

**Signature:** `progress\span() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### column

The footprints.

**Signature:** `progress\column() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### fallback

Fallback is the loader to draw when the terminal cannot show Unicode.

**Signature:** `progress\fallback() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

