# `json`

JSON encode/decode.

> **Naming convention:** Buzz reaches modules off the `import "magus/extra"` aggregate in `camelCase` (`extra.json.someMethod`).

## Methods

### `parse`

Decode a JSON string into a Lua value (table, string, number, or boolean).

**Signature:** `extra.json.parse(s) → any`

**Also in Buzz's stdlib:** `serialize.jsonDecode` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** any

### `stringify`

Encode a Lua value as a JSON string.

**Signature:** `extra.json.stringify(value) → string`

**Also in Buzz's stdlib:** `serialize.jsonEncode` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `value` | `any` |  | |

**Returns:** string

