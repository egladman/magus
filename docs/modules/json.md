# `json`

JSON encode/decode.

> **Naming convention:** Buzz reaches modules off the `import "magus/extra"` aggregate in `camelCase` (`extra.json.someMethod`).

## Methods

### `parse`

Decode a JSON string into a value (map, list, string, number, or boolean).

**Signature:** `extra.json.parse(s) → any`

**Also in Buzz's stdlib:** `serialize.jsonDecode` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** any

### `stringify`

Encode a value as a JSON string. With no indent (or "") the output is compact; pass an indent string (e.g. "  " or "\t") for pretty, multi-line output.

**Signature:** `extra.json.stringify(value, [indent]) → string`

**Also in Buzz's stdlib:** `serialize.jsonEncode` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `value` | `any` |  | |
| `indent` | `string` | yes | |

**Returns:** string

