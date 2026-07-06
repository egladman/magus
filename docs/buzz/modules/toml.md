---
title: toml module
aliases: [modules/toml]
description: TOML parse and stringify (TOML 1.0 via pelletier/go-toml/v2).
tags: [toml, module, stdlib, magusfile]
---

# toml

TOML parse and stringify (TOML 1.0 via pelletier/go-toml/v2).

> **Naming convention:** import the module under its bare name (`import "toml"`) and call methods in `camelCase` (`toml.someMethod`).

## Methods

### parse

Decode a TOML document into a value (tables become maps, arrays become lists, plus strings, numbers, bools, and datetimes); errors on invalid input.

**Signature:** `toml.parse(source) → any` · [source](https://github.com/egladman/magus/blob/main/std/toml.go#L41)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `source` | `string` |  | |

**Returns:** any

### stringify

Encode a value to a TOML string; the top level must be a table/map, as TOML requires. Errors on unencodable input.

**Signature:** `toml.stringify(value) → string` · [source](https://github.com/egladman/magus/blob/main/std/toml.go#L50)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `value` | `any` |  | |

**Returns:** string

