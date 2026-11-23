---
title: progress module
generated_from: reference/buzz/
aliases: [modules/progress]
description: Bindings for the Go package github.com/egladman/magus/libs/termkit/progress, generated from its exported API.
tags: [progress, module, stdlib, magusfile]
---

# progress

Bindings for the Go package github.com/egladman/magus/libs/termkit/progress, generated from its exported API. A value with state is an int handle. Record methods belong to typed objects. Writers are standard error and readers standard input.

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

### catalog

Catalog returns every loader in display order.

**Signature:** `progress\catalog() -> any` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** any

### newRotation

NewRotation builds the selected rotation loader.

**Signature:** `progress\newRotation(name) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `name`    | `string` |          |             |

**Returns:** int

### newGlyph

NewGlyph builds the selected glyph loader.

**Signature:** `progress\newGlyph(name) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `name`    | `string` |          |             |

**Returns:** int

### newStrip

NewStrip builds the selected strip loader.

**Signature:** `progress\newStrip(name) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `name`    | `string` |          |             |

**Returns:** int

### newText

NewText builds the selected text loader.

**Signature:** `progress\newText(name) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `name`    | `string` |          |             |

**Returns:** int

### newSpan

NewSpan builds the selected span loader.

**Signature:** `progress\newSpan(name) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `name`    | `string` |          |             |

**Returns:** int

### newColumn

NewColumn builds the selected column loader.

**Signature:** `progress\newColumn(name) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `name`    | `string` |          |             |

**Returns:** int

### startSpinner

StartSpinner starts a loader turning beside label on out and returns at once; the caller does its blocking work and then calls [Spinner.Stop].

**Signature:** `progress\startSpinner(p, label) -> int` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `p`       | `int`    |          |             |
| `label`   | `string` |          |             |

**Returns:** int

### spinnerStop

Stop erases the line and returns once nothing more will be written, so the caller's next write cannot interleave with a frame.

**Signature:** `progress\spinnerStop(spinner)` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `spinner` | `int` |          |             |

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

### rotationComet

The value of RotationComet.

**Signature:** `progress\rotationComet() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### rotationSquare

The value of RotationSquare.

**Signature:** `progress\rotationSquare() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### rotationClock

The value of RotationClock.

**Signature:** `progress\rotationClock() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### rotationRing

The value of RotationRing.

**Signature:** `progress\rotationRing() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### rotationPinwheel

The value of RotationPinwheel.

**Signature:** `progress\rotationPinwheel() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### rotationInfinity

The value of RotationInfinity.

**Signature:** `progress\rotationInfinity() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### glyphDots

The value of GlyphDots.

**Signature:** `progress\glyphDots() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### glyphOrbit

The value of GlyphOrbit.

**Signature:** `progress\glyphOrbit() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### glyphPulse

The value of GlyphPulse.

**Signature:** `progress\glyphPulse() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### glyphTriangle

The value of GlyphTriangle.

**Signature:** `progress\glyphTriangle() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### glyphSpark

The value of GlyphSpark.

**Signature:** `progress\glyphSpark() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### glyphLine

The value of GlyphLine.

**Signature:** `progress\glyphLine() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### stripScanner

The value of StripScanner.

**Signature:** `progress\stripScanner() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### stripEqualizer

The value of StripEqualizer.

**Signature:** `progress\stripEqualizer() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### stripShade

The value of StripShade.

**Signature:** `progress\stripShade() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### stripSnake

The value of StripSnake.

**Signature:** `progress\stripSnake() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### stripSine

The value of StripSine.

**Signature:** `progress\stripSine() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### stripDots

The value of StripDots.

**Signature:** `progress\stripDots() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### textShimmer

The value of TextShimmer.

**Signature:** `progress\textShimmer() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### textTypewriter

The value of TextTypewriter.

**Signature:** `progress\textTypewriter() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### spanComet

The value of SpanComet.

**Signature:** `progress\spanComet() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### spanProgress

The value of SpanProgress.

**Signature:** `progress\spanProgress() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### columnFill

The value of ColumnFill.

**Signature:** `progress\columnFill() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

### columnElevator

The value of ColumnElevator.

**Signature:** `progress\columnElevator() -> string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/progress/module.go)

**Returns:** string

