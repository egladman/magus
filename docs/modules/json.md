# `json`

JSON encode/decode.

> **Naming convention:** Teal/Lua binds each module per-import in `snake_case` (`local json = require("magus.extra.json")`, then `json.some_method`). Buzz reaches them off the `import "magus/extra"` aggregate in `camelCase` (`extra.json.someMethod`).

## Methods

### `parse`

Decode a JSON string into a Lua value (table, string, number, or boolean).

**Signature (Teal):** `json.parse(s) → any`

**Signature (Buzz):** `extra.json.parse(s) → any`

**Also in Buzz's stdlib:** `serialize.jsonDecode` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** any

### `stringify`

Encode a Lua value as a JSON string.

**Signature (Teal):** `json.stringify(value) → string`

**Signature (Buzz):** `extra.json.stringify(value) → string`

**Also in Buzz's stdlib:** `serialize.jsonEncode` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `value` | `any` |  | |

**Returns:** string

