---
title: json module
description: Parse JSON strings to values and stringify values to JSON. Compact or pretty output via optional indent. Sandbox-aware form over Buzz's serialize.
tags: [json, parse json, stringify, encode, decode, serialize, magus stdlib]
---

# json

JSON encode/decode.

> **Naming convention:** import the module under its bare name (`import "json"`) and call methods in `camelCase` (`json.someMethod`).

## Methods

### parse

Decode a JSON string into a value (map, list, string, number, or boolean).

**Signature:** `json.parse(s) → any`[^buzz-stdlib-json-parse] · [source](https://github.com/egladman/magus/blob/main/std/json.go#L40)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** any

### stringify

Encode a value as a JSON string. With no indent (or "") the output is compact; pass an indent string (e.g. "  " or "\t") for pretty, multi-line output.

**Signature:** `json.stringify(value, [indent]) → string`[^buzz-stdlib-json-stringify] · [source](https://github.com/egladman/magus/blob/main/std/json.go#L53)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `value` | `any` |  | |
| `indent` | `string` | yes | |

**Returns:** string

[^buzz-stdlib-json-parse]: `json.parse` is also in Buzz's standard library (`serialize.jsonDecode`); the magus form is sandbox-aware.
[^buzz-stdlib-json-stringify]: `json.stringify` is also in Buzz's standard library (`serialize.jsonEncode`); the magus form is sandbox-aware.
