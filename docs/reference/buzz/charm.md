---
title: charm module
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

**Signature:** `charm\append(vals) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L197)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### prepend

Insert vals at the front of the argv, in order.

**Signature:** `charm\prepend(vals) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L206)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### after

Insert vals immediately after the first argv element equal to anchor.

**Signature:** `charm\after(argv, anchor, vals) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L211)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### before

Insert vals immediately before the first argv element equal to anchor.

**Signature:** `charm\before(argv, anchor, vals) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L220)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### set

Replace the first argv element equal to anchor with val.

**Signature:** `charm\set(argv, anchor, val) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L229)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |
| `val` | `string` |  | |

**Returns:** map[string]any

### drop

Drop (remove) the first argv element equal to anchor.

**Signature:** `charm\drop(argv, anchor) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L238)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |

**Returns:** map[string]any

### afterFunc

Insert vals after the first argv element for which fn(s) is truthy.

**Signature:** `charm\afterFunc(argv, fn, vals) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L247)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L17) |  | |
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### beforeFunc

Insert vals before the first argv element for which fn(s) is truthy.

**Signature:** `charm\beforeFunc(argv, fn, vals) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L256)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L17) |  | |
| `vals` | `[]string` |  | |

**Returns:** map[string]any

### setFunc

Replace the first argv element for which fn(s) is truthy with val.

**Signature:** `charm\setFunc(argv, fn, val) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L265)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L17) |  | |
| `val` | `string` |  | |

**Returns:** map[string]any

### dropFunc

Drop (remove) the first argv element for which fn(s) is truthy.

**Signature:** `charm\dropFunc(argv, fn) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L274)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L17) |  | |

**Returns:** map[string]any

### path

Return the JSON Pointer ("/N") of the first argv element equal to anchor - the index, auto-calculated, for hand-built move/copy/test ops.

**Signature:** `charm\path(argv, anchor) → string` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L283)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |

**Returns:** string

### pathFunc

Return the JSON Pointer ("/N") of the first argv element for which fn(s) is truthy.

**Signature:** `charm\pathFunc(argv, fn) → string` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L292)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L17) |  | |

**Returns:** string

### move

Move the first argv element equal to anchor to the JSON Pointer to ("/-" end, "/0" front, or charm.path(...)).

**Signature:** `charm\move(argv, anchor, to) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L310)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |
| `to` | `string` |  | |

**Returns:** map[string]any

### moveFunc

Move the first argv element for which fn(s) is truthy to the JSON Pointer to.

**Signature:** `charm\moveFunc(argv, fn, to) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L322)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L17) |  | |
| `to` | `string` |  | |

**Returns:** map[string]any

### copy

Copy the first argv element equal to anchor to the JSON Pointer to ("/-" end, "/0" front, or charm.path(...)).

**Signature:** `charm\copy(argv, anchor, to) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L334)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |
| `to` | `string` |  | |

**Returns:** map[string]any

### copyFunc

Copy the first argv element for which fn(s) is truthy to the JSON Pointer to.

**Signature:** `charm\copyFunc(argv, fn, to) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L346)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L17) |  | |
| `to` | `string` |  | |

**Returns:** map[string]any

### test

Guard: assert the first argv element equal to anchor is still at its position when the patch applies (else the run errors).

**Signature:** `charm\test(argv, anchor) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L359)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `anchor` | `string` |  | |

**Returns:** map[string]any

### testFunc

Guard: assert the first argv element for which fn(s) is truthy is still at its position when the patch applies.

**Signature:** `charm\testFunc(argv, fn) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/charm.go#L368)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `argv` | `[]string` |  | |
| `fn` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L17) |  | |

**Returns:** map[string]any

