---
title: recorder module
generated_from: reference/buzz/
aliases: [modules/recorder]
description: Bindings for the Go package github.com/egladman/magus/libs/termkit/recorder, generated from its exported API.
tags: [recorder, module, stdlib, magusfile]
---

# recorder

Bindings for the Go package github.com/egladman/magus/libs/termkit/recorder, generated from its exported API. A value with state is an int handle. Record methods belong to typed objects. Writers are standard error and readers standard input.

> **Naming convention:** import the module under its bare name (`import "recorder"`), reach members with a backslash, and call methods in `camelCase`: `recorder\someMethod`.

## Methods

### commands

Commands keeps startup bytes on the first command's frame.

**Signature:** `recorder\commands(capture, prompt) -> []string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/recorder/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `capture` | `string` |          |             |
| `prompt`  | `string` |          |             |

**Returns:** []string

### marked

Marked discards teardown after the final frame marker.

**Signature:** `recorder\marked(capture, marker) -> []string` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/recorder/module.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `capture` | `string` |          |             |
| `marker`  | `string` |          |             |

**Returns:** []string

### animate

Animate replays one cumulative terminal and gives all frames the same height.

**Signature:** `recorder\animate(segments, width, height, pace, theme) -> result\Result::<str>` - [source](https://github.com/egladman/magus/blob/main/std/termkit/gen/recorder/module.go)

| Parameter  | Type             | Optional | Description |
| ---------- | ---------------- | -------- | ----------- |
| `segments` | `[]string`       |          |             |
| `width`    | `int`            |          |             |
| `height`   | `int`            |          |             |
| `pace`     | `map[string]any` |          |             |
| `theme`    | `map[string]any` |          |             |

**Returns:** map[string]any

