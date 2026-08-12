---
title: base64 module
aliases: [modules/base64]
description: Base64 text codec (standard and URL-safe, both padded).
tags: [base64, module, stdlib, magusfile]
---

# base64

Base64 text codec (standard and URL-safe, both padded).

> **Naming convention:** import the module under its bare name (`import "base64"`), reach members with a backslash, and call methods in `camelCase`: `base64\someMethod`.

## Methods

### encode

Encode data as standard (padded) base64.

**Signature:** `base64\encode(data) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding/base64/base64.go#L71)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### decode

Decode a standard (padded) base64 string; errors on malformed input.

**Signature:** `base64\decode(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding/base64/base64.go#L76)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### urlEncode

Encode data as URL-safe (padded) base64.

**Signature:** `base64\urlEncode(data) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding/base64/base64.go#L85)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### urlDecode

Decode a URL-safe (padded) base64 string; errors on malformed input.

**Signature:** `base64\urlDecode(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding/base64/base64.go#L90)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

