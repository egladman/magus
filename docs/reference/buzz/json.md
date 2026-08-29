---
title: json module
generated_from: reference/buzz/
aliases: [modules/json]
description: JSON encode/decode.
tags: [json, module, stdlib, magusfile]
---

# json

JSON encode/decode.

> **Naming convention:** import the module under its bare name (`import "json"`), reach members with a backslash, and call methods in `camelCase`: `json\someMethod`.

<!-- -->

> [!NOTE]
> The examples below are reference-only. `json` performs real IO (filesystem, process, network, or environment access) that the in-browser playground's sandbox cannot provide, so it is not registered there and its examples have no Run button. Pure-compute modules such as `strings` and `json` run their examples live in the page.

## Methods

### parse

Decode a JSON string into a value (map, list, string, number, or boolean).

**Signature:** `json\parse(s) -> any`[^buzz-stdlib-json-parse] - [source](https://github.com/egladman/magus/blob/main/std/encoding/json/json.go#L47)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** any

**Example:**

```buzz
import "std";
import "encoding/json";

final v = json\parse("\{\"name\": \"api\", \"port\": 8080\}");
std\print(v["name"]);
std\print(v["port"]);
// -> api
// -> 8080
```

### stringify

Encode a value as a JSON string. With no indent (or "") the output is compact; pass an indent string (e.g. "  " or "\t") for pretty, multi-line output.

**Signature:** `json\stringify(value, [indent]) -> string`[^buzz-stdlib-json-stringify] - [source](https://github.com/egladman/magus/blob/main/std/encoding/json/json.go#L60)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `value` | `any` |  | |
| `indent` | `string` | yes | |

**Returns:** string

**Example:**

```buzz
import "std";
import "encoding/json";

final config = { "target": "build", "parallel": true };
std\print(json\stringify(config));
// -> {"parallel":true,"target":"build"}

// Pretty-printed with two-space indent:
std\print(json\stringify(config, "  "));
```

[^buzz-stdlib-json-parse]: `json\parse` is also in Buzz's standard library (`serialize.jsonDecode`); the magus form is sandbox-aware.
[^buzz-stdlib-json-stringify]: `json\stringify` is also in Buzz's standard library (`serialize.jsonEncode`); the magus form is sandbox-aware.
