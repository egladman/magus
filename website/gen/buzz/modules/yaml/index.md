---
title: yaml module
aliases: [modules/yaml]
description: YAML parse and stringify (YAML 1.2 via gopkg.in/yaml.v3).
tags: [yaml, module, stdlib, magusfile]
---

# yaml

YAML parse and stringify (YAML 1.2 via gopkg.in/yaml.v3).

> **Naming convention:** import the module under its bare name (`import "yaml"`) and call methods in `camelCase` (`yaml.someMethod`).

## Methods

### parse

Decode a YAML string into a value (maps, lists, strings, numbers, bools, null); errors on invalid input.

**Signature:** `yaml.parse(source) → any` · [source](https://github.com/egladman/magus/blob/main/std/yaml.go#L39)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `source` | `string` |  | |

**Returns:** any

### stringify

Encode a value to a YAML string; errors on unencodable input.

**Signature:** `yaml.stringify(value) → string` · [source](https://github.com/egladman/magus/blob/main/std/yaml.go#L48)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `value` | `any` |  | |

**Returns:** string

