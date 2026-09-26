---
title: yaml module
generated_from: reference/buzz/
aliases: [modules/yaml]
description: YAML parse and stringify (YAML 1.2 via gopkg.in/yaml.v3).
tags: [yaml, module, stdlib, magusfile]
---

# yaml

YAML parse and stringify (YAML 1.2 via gopkg.in/yaml.v3).

> **Naming convention:** import the module under its bare name (`import "yaml"`), reach members with a backslash, and call methods in `camelCase`: `yaml\someMethod`.

## Methods

### parse

Decode a YAML string into a value (maps, lists, strings, numbers, bools, null); errors on invalid input.

**Signature:** `yaml\parse(source) -> any` - [source](https://github.com/egladman/magus/blob/main/std/encoding/yaml/yaml.go#L62)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `source`  | `string` |          |             |

**Returns:** any

### positions

Report where each value of a YAML document starts, as {lines, columns}: 1-based, each keyed by the value's JSON pointer ("" for the root, /jobs/build/steps/0 for a list item, ~0 and ~1 escaping ~ and / in a key). A mapping entry is keyed at its value, so /jobs/build is where that job's body starts. An alias is keyed where it appears and not followed. What parse returns carries no positions; this is how a check over it points at a line. Errors on invalid input.

**Signature:** `yaml\positions(source) -> YamlPositions` - [source](https://github.com/egladman/magus/blob/main/std/encoding/yaml/yaml.go#L72)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `source`  | `string` |          |             |

**Returns:** map[string]any

### stringify

Encode a value to a YAML string; errors on unencodable input.

**Signature:** `yaml\stringify(value) -> string` - [source](https://github.com/egladman/magus/blob/main/std/encoding/yaml/yaml.go#L106)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `value`   | `any` |          |             |

**Returns:** string

