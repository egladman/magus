---
title: hex module
aliases: [modules/hex]
description: Hex text codec.
tags: [hex, module, stdlib, magusfile]
---

# hex

Hex text codec.

> **Naming convention:** import the module under its bare name (`import "hex"`), reach members with a backslash, and call methods in `camelCase`: `hex\someMethod`.

## Methods

### encode

Encode data as lowercase hex.

**Signature:** `hex\encode(data) → string` · [source](https://github.com/egladman/magus/blob/main/std/hex.go#L40)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### decode

Decode a hex string; errors on malformed input.

**Signature:** `hex\decode(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/hex.go#L45)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

