# `json`

JSON encode/decode.

> **Naming convention:** import the module under its bare name (`import "json"`) and call methods in `camelCase` (`json.someMethod`).

## Methods

### `parse`

Decode a JSON string into a value (map, list, string, number, or boolean).

**Signature:** `json.parse(s) → any`

**Also in Buzz's stdlib:** `serialize.jsonDecode` — the magus form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** any

### `stringify`

Encode a value as a JSON string. With no indent (or "") the output is compact; pass an indent string (e.g. "  " or "\t") for pretty, multi-line output.

**Signature:** `json.stringify(value, [indent]) → string`

**Also in Buzz's stdlib:** `serialize.jsonEncode` — the magus form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `value` | `any` |  | |
| `indent` | `string` | yes | |

**Returns:** string

