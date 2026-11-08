---
title: screen module
generated_from: reference/buzz/
aliases: [modules/screen]
description: Bindings for the Go package github.com/egladman/magus/libs/termkit/screen, generated from its exported API.
tags: [screen, module, stdlib, magusfile]
---

# screen

Bindings for the Go package github.com/egladman/magus/libs/termkit/screen, generated from its exported API. A value with state is an int handle and a record is a map. Writers are standard error and readers standard input.

> **Naming convention:** import the module under its bare name (`import "screen"`), reach members with a backslash, and call methods in `camelCase`: `screen\someMethod`.

## Methods

### new

New returns a Terminal of the given size, cursor at the top left and the whole grid scrollable.

**Signature:** `screen\new(width, height) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `width`   | `int` |          |             |
| `height`  | `int` |          |             |

**Returns:** int

### terminalFd

Fd and Write let a test hand a Terminal to code that expects one, so it drives the real code path.

**Signature:** `screen\terminalFd(terminal) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `terminal` | `int` |          |             |

**Returns:** int

### terminalWrite

Calls Terminal.Write.

**Signature:** `screen\terminalWrite(terminal, p) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter  | Type     | Optional | Description |
| ---------- | -------- | -------- | ----------- |
| `terminal` | `int`    |          |             |
| `p`        | `[]byte` |          |             |

**Returns:** int

### terminalRows

Rows renders the grid as right-trimmed text, one string per terminal row.

**Signature:** `screen\terminalRows(terminal) -> []string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `terminal` | `int` |          |             |

**Returns:** []string

### terminalString

String renders the whole screen, for a failure message that shows what the user would have been looking at.

**Signature:** `screen\terminalString(terminal) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `terminal` | `int` |          |             |

**Returns:** string

### terminalRow

Row returns one 1-based row's text.

**Signature:** `screen\terminalRow(terminal, n) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `terminal` | `int` |          |             |
| `n`        | `int` |          |             |

**Returns:** string

### terminalStyleAt

StyleAt reports the SGR parameters in force at a cell, so a test can assert that a warning is yellow without matching escape bytes.

**Signature:** `screen\terminalStyleAt(terminal, row, col) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `terminal` | `int` |          |             |
| `row`      | `int` |          |             |
| `col`      | `int` |          |             |

**Returns:** string

### terminalSnapshot

Snapshot returns an independent copy of the screen as it is right now.

**Signature:** `screen\terminalSnapshot(terminal) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `terminal` | `int` |          |             |

**Returns:** int

### terminalLastUsedRow

LastUsedRow returns the 1-based row of the last row with any text on it, or 0 when the screen is blank.

**Signature:** `screen\terminalLastUsedRow(terminal) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `terminal` | `int` |          |             |

**Returns:** int

### terminalCrop

Crop returns a copy holding only the first rows rows.

**Signature:** `screen\terminalCrop(terminal, rows) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `terminal` | `int` |          |             |
| `rows`     | `int` |          |             |

**Returns:** int

### terminalCursor

Cursor reports where the cursor is, in 1-based terminal coordinates.

**Signature:** `screen\terminalCursor(terminal) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `terminal` | `int` |          |             |

**Returns:** any

### terminalScrollRegion

ScrollRegion reports the rows the terminal will scroll, 1-based inclusive.

**Signature:** `screen\terminalScrollRegion(terminal) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `terminal` | `int` |          |             |

**Returns:** any

### terminalScrolled

Scrolled reports how many lines have been pushed off the top of the scroll region, so a test can assert a repaint did not disturb the transcript.

**Signature:** `screen\terminalScrolled(terminal) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `terminal` | `int` |          |             |

**Returns:** int

### terminalFindRow

FindRow returns the 1-based row whose text contains substr, or 0.

**Signature:** `screen\terminalFindRow(terminal, substr) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter  | Type     | Optional | Description |
| ---------- | -------- | -------- | ----------- |
| `terminal` | `int`    |          |             |
| `substr`   | `string` |          |             |

**Returns:** int

### terminalTrailingSpaceRows

TrailingSpaceRows returns the 1-based rows whose last written cell is a space.

**Signature:** `screen\terminalTrailingSpaceRows(terminal) -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `terminal` | `int` |          |             |

**Returns:** any

### terminalSvg

SVG renders what the terminal currently shows as a standalone SVG picture.

**Signature:** `screen\terminalSvg(terminal, opts) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter  | Type             | Optional | Description |
| ---------- | ---------------- | -------- | ----------- |
| `terminal` | `int`            |          |             |
| `opts`     | `map[string]any` |          |             |

**Returns:** string

### variantPath

VariantPath names the file one variant of a picture is written to, given the unsuffixed name and the variant's suffix.

**Signature:** `screen\variantPath(path, suffix) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `path`    | `string` |          |             |
| `suffix`  | `string` |          |             |

**Returns:** string

### animate

Animate renders a sequence of frames as one self-contained animated SVG.

**Signature:** `screen\animate(frames, holds, opts) -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `frames`  | `any`            |          |             |
| `holds`   | `any`            |          |             |
| `opts`    | `map[string]any` |          |             |

**Returns:** string

### darkTheme

DarkTheme is the default: a modern terminal profile rather than the sixteen ANSI primaries, which are harsh at small sizes on a page.

**Signature:** `screen\darkTheme() -> map[string]any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

**Returns:** map[string]any

### lightTheme

LightTheme renders on a page that is not dark.

**Signature:** `screen\lightTheme() -> map[string]any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

**Returns:** map[string]any

### themeVariants

ThemeVariants is every palette a committed picture ships in.

**Signature:** `screen\themeVariants() -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/screen/module.go)

**Returns:** any

