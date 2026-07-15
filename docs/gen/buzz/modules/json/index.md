---
title: json module
aliases: [modules/json]
description: JSON encode/decode.
tags: [json, module, stdlib, magusfile]
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

**Example:**

<!-- run -->
```buzz
import "std";
import "json";

final v = json.parse("\{\"name\": \"api\", \"port\": 8080\}");
std.print(v["name"]);
std.print(v["port"]);
// -> api
// -> 8080
```

### stringify

Encode a value as a JSON string. With no indent (or "") the output is compact; pass an indent string (e.g. "  " or "\t") for pretty, multi-line output.

**Signature:** `json.stringify(value, [indent]) → string`[^buzz-stdlib-json-stringify] · [source](https://github.com/egladman/magus/blob/main/std/json.go#L53)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `value` | `any` |  | |
| `indent` | `string` | yes | |

**Returns:** string

**Example:**

<!-- run -->
```buzz
import "std";
import "json";

final config = { "target": "build", "parallel": true };
std.print(json.stringify(config));
// -> {"parallel":true,"target":"build"}

// Pretty-printed with two-space indent:
std.print(json.stringify(config, "  "));
```

[^buzz-stdlib-json-parse]: `json.parse` is also in Buzz's standard library (`serialize.jsonDecode`); the magus form is sandbox-aware.
[^buzz-stdlib-json-stringify]: `json.stringify` is also in Buzz's standard library (`serialize.jsonEncode`); the magus form is sandbox-aware.
