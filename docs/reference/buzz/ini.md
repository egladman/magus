---
title: ini module
generated_from: reference/buzz/
aliases: [modules/ini]
description: INI/properties config parsing and rendering (.npmrc, .gitconfig, .editorconfig).
tags: [ini, module, stdlib, magusfile]
---

# ini

INI/properties config parsing and rendering (.npmrc, .gitconfig, .editorconfig).

> **Naming convention:** import the module under its bare name (`import "ini"`), reach members with a backslash, and call methods in `camelCase`: `ini\someMethod`.

## Methods

### parse

Parse INI text into {section: {key: value}}. Entries before the first [section] header are under the "" key, which is where a flat file like .npmrc puts everything. Values are always strings; a repeated key takes the last value.

**Signature:** `ini\parse(source) → map[string]map[string]string` · [source](https://github.com/egladman/magus/blob/main/std/encoding/ini/ini.go#L74)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `source` | `string` |  | |

**Returns:** map[string]map[string]string

### stringify

Render {section: {key: value}} back to INI text. The "" section is written first with no header, then the rest sorted by name with their keys sorted, so the output is byte-stable and diffs cleanly.

**Signature:** `ini\stringify(sections) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding/ini/ini.go#L124)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `sections` | `map[string]map[string]string` |  | |

**Returns:** string

