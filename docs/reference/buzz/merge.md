---
title: merge module
generated_from: reference/buzz/
aliases: [modules/merge]
description: Combine two values.
tags: [merge, module, stdlib, magusfile]
---

# merge

Combine two values. shallow replaces one level of an object; deep recurses into objects.

> **Naming convention:** import the module under its bare name (`import "merge"`), reach members with a backslash, and call methods in `camelCase`: `merge\someMethod`.

## Methods

### shallow

Return a new value with overlay applied one level deep. When both are objects, overlay's keys replace base's and nested objects are replaced whole, so a sibling key inside a nested object is dropped. Any other overlay replaces base entirely. Neither input is modified.

**Signature:** `merge\shallow(base, overlay) -> any` - [source](https://github.com/egladman/magus/blob/main/std/merge.go#L72)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `base`    | `any` |          |             |
| `overlay` | `any` |          |             |

**Returns:** any

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "encoding/json";
import "merge";

final base = { "name": "api", "port": 8080 };
final overlay = { "port": 9090 };
std\print(json\stringify(merge\shallow(base, overlay: overlay)) catch "");
// -> {"name":"api","port":9090}
```

### deep

Return a new value with overlay applied through every nested object. Objects merge key by key; an array, a scalar, or null in overlay replaces the base value at that key, because a managed list is already the whole list. Neither input is modified.

**Signature:** `merge\deep(base, overlay) -> any` - [source](https://github.com/egladman/magus/blob/main/std/merge.go#L77)

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `base`    | `any` |          |             |
| `overlay` | `any` |          |             |

**Returns:** any

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "encoding/json";
import "merge";

// "after" is a sibling of the key overlay patches, so it survives.
// "before" is an array, so overlay replaces it instead of appending.
final base = { "kept": true, "hooks": { "before": ["old"], "after": ["theirs"] } };
final overlay = { "hooks": { "before": ["magus"] } };
std\print(json\stringify(merge\deep(base, overlay: overlay)) catch "");
```

### json

Deep-merge two JSON documents and return the indented text, with a trailing newline. Integers keep the digits that were in the text, which merge\deep cannot promise once a number has been a Buzz value. Objects merge key by key; arrays and other values are replaced by overlay. Object keys are emitted sorted, so the result does not keep base's key order.

**Signature:** `merge\json(base, overlay) -> string` - [source](https://github.com/egladman/magus/blob/main/std/merge.go#L83)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `base`    | `string` |          |             |
| `overlay` | `string` |          |             |

**Returns:** string

**Example:**

<!-- magus-run -->
```buzz
import "std";
import "merge";

// The integer is past 2^53. Merging the text keeps those digits;
// parsing both sides to values first would not.
final base = "\{\"large\": 9007199254740993, \"hooks\": \{\"after\": [\"theirs\"]\}\}";
final overlay = "\{\"hooks\": \{\"before\": [\"magus\"]\}\}";
std\print(merge\json(base, overlay: overlay) catch "");
```

