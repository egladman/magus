---
title: charm module
generated_from: reference/buzz/
aliases: [modules/charm]
description: "Constructors for charm values: RFC 6902 JSON Patches over a target's argv (see docs/charms.md)."
tags: [charm, module, stdlib, magusfile]
---

# charm

Constructors for charm values: RFC 6902 JSON Patches over a target's argv (see docs/charms.md).

> **Naming convention:** import the module under its bare name (`import "charm"`), reach members with a backslash, and call methods in `camelCase`: `charm\someMethod`.

## Methods

### append

Append vals to the end of the argv.

**Signature:** `charm\append(vals) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `vals`    | `[]string` |          |             |

**Returns:** map[string]any

### prepend

Insert vals at the front of the argv, in order.

**Signature:** `charm\prepend(vals) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `vals`    | `[]string` |          |             |

**Returns:** map[string]any

### after

Insert vals immediately after the first argv element equal to anchor.

**Signature:** `charm\after(argv, anchor, vals) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `argv`    | `[]string` |          |             |
| `anchor`  | `string`   |          |             |
| `vals`    | `[]string` |          |             |

**Returns:** map[string]any

### before

Insert vals immediately before the first argv element equal to anchor.

**Signature:** `charm\before(argv, anchor, vals) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `argv`    | `[]string` |          |             |
| `anchor`  | `string`   |          |             |
| `vals`    | `[]string` |          |             |

**Returns:** map[string]any

### set

Replace the first argv element equal to anchor with val.

**Signature:** `charm\set(argv, anchor, val) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `argv`    | `[]string` |          |             |
| `anchor`  | `string`   |          |             |
| `val`     | `string`   |          |             |

**Returns:** map[string]any

### drop

Drop (remove) the first argv element equal to anchor.

**Signature:** `charm\drop(argv, anchor) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `argv`    | `[]string` |          |             |
| `anchor`  | `string`   |          |             |

**Returns:** map[string]any

### afterFunc

Insert vals after the first argv element for which fn(s) returns true.

**Signature:** `charm\afterFunc(argv, fn, vals) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type                                          | Optional | Description |
| --------- | --------------------------------------------- | -------- | ----------- |
| `argv`    | `[]string`                                    |          |             |
| `fn`      | `func(context.Context, string) (bool, error)` |          |             |
| `vals`    | `[]string`                                    |          |             |

**Returns:** map[string]any

### beforeFunc

Insert vals before the first argv element for which fn(s) returns true.

**Signature:** `charm\beforeFunc(argv, fn, vals) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type                                          | Optional | Description |
| --------- | --------------------------------------------- | -------- | ----------- |
| `argv`    | `[]string`                                    |          |             |
| `fn`      | `func(context.Context, string) (bool, error)` |          |             |
| `vals`    | `[]string`                                    |          |             |

**Returns:** map[string]any

### setFunc

Replace the first argv element for which fn(s) returns true with val.

**Signature:** `charm\setFunc(argv, fn, val) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type                                          | Optional | Description |
| --------- | --------------------------------------------- | -------- | ----------- |
| `argv`    | `[]string`                                    |          |             |
| `fn`      | `func(context.Context, string) (bool, error)` |          |             |
| `val`     | `string`                                      |          |             |

**Returns:** map[string]any

### dropFunc

Drop (remove) the first argv element for which fn(s) returns true.

**Signature:** `charm\dropFunc(argv, fn) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type                                          | Optional | Description |
| --------- | --------------------------------------------- | -------- | ----------- |
| `argv`    | `[]string`                                    |          |             |
| `fn`      | `func(context.Context, string) (bool, error)` |          |             |

**Returns:** map[string]any

### path

Return the JSON Pointer ("/N") of the first argv element equal to anchor - the index, auto-calculated, for hand-built move/copy/test ops.

**Signature:** `charm\path(argv, anchor) -> string` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `argv`    | `[]string` |          |             |
| `anchor`  | `string`   |          |             |

**Returns:** string

### pathFunc

Return the JSON Pointer ("/N") of the first argv element for which fn(s) returns true.

**Signature:** `charm\pathFunc(argv, fn) -> string` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type                                          | Optional | Description |
| --------- | --------------------------------------------- | -------- | ----------- |
| `argv`    | `[]string`                                    |          |             |
| `fn`      | `func(context.Context, string) (bool, error)` |          |             |

**Returns:** string

### move

Move the first argv element equal to anchor to the JSON Pointer to ("/-" end, "/0" front, or charm\path(...)).

**Signature:** `charm\move(argv, anchor, to) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `argv`    | `[]string` |          |             |
| `anchor`  | `string`   |          |             |
| `to`      | `string`   |          |             |

**Returns:** map[string]any

### moveFunc

Move the first argv element for which fn(s) returns true to the JSON Pointer to.

**Signature:** `charm\moveFunc(argv, fn, to) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type                                          | Optional | Description |
| --------- | --------------------------------------------- | -------- | ----------- |
| `argv`    | `[]string`                                    |          |             |
| `fn`      | `func(context.Context, string) (bool, error)` |          |             |
| `to`      | `string`                                      |          |             |

**Returns:** map[string]any

### copy

Copy the first argv element equal to anchor to the JSON Pointer to ("/-" end, "/0" front, or charm\path(...)).

**Signature:** `charm\copy(argv, anchor, to) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `argv`    | `[]string` |          |             |
| `anchor`  | `string`   |          |             |
| `to`      | `string`   |          |             |

**Returns:** map[string]any

### copyFunc

Copy the first argv element for which fn(s) returns true to the JSON Pointer to.

**Signature:** `charm\copyFunc(argv, fn, to) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type                                          | Optional | Description |
| --------- | --------------------------------------------- | -------- | ----------- |
| `argv`    | `[]string`                                    |          |             |
| `fn`      | `func(context.Context, string) (bool, error)` |          |             |
| `to`      | `string`                                      |          |             |

**Returns:** map[string]any

### test

Guard: assert the first argv element equal to anchor is still at its position when the patch applies (else the run errors).

**Signature:** `charm\test(argv, anchor) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `argv`    | `[]string` |          |             |
| `anchor`  | `string`   |          |             |

**Returns:** map[string]any

### testFunc

Guard: assert the first argv element for which fn(s) returns true is still at its position when the patch applies.

**Signature:** `charm\testFunc(argv, fn) -> Charm` - [source](https://github.com/egladman/magus/blob/main/std/charm.go)

| Parameter | Type                                          | Optional | Description |
| --------- | --------------------------------------------- | -------- | ----------- |
| `argv`    | `[]string`                                    |          |             |
| `fn`      | `func(context.Context, string) (bool, error)` |          |             |

**Returns:** map[string]any

