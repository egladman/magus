# `encoding`

Base64/hex/URL text codecs.

> **Naming convention:** import the module under its bare name (`import "encoding"`) and call methods in `camelCase` (`encoding.someMethod`).

## Methods

### `base64_encode`

Encode data as standard (padded) base64.

**Signature:** `encoding.base64Encode(data) → string`

**Also in Buzz's stdlib:** `str.encodeBase64 (built-in string method)` — the magus form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### `base64_decode`

Decode a standard (padded) base64 string; errors on malformed input.

**Signature:** `encoding.base64Decode(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### `base64url_encode`

Encode data as URL-safe (padded) base64.

**Signature:** `encoding.base64urlEncode(data) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### `base64url_decode`

Decode a URL-safe (padded) base64 string; errors on malformed input.

**Signature:** `encoding.base64urlDecode(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### `hex_encode`

Encode data as lowercase hex.

**Signature:** `encoding.hexEncode(data) → string`

**Also in Buzz's stdlib:** `str.hex (built-in string method)` — the magus form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### `hex_decode`

Decode a hex string; errors on malformed input.

**Signature:** `encoding.hexDecode(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### `url_encode`

Percent-encode s for use in a URL query component.

**Signature:** `encoding.urlEncode(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### `url_decode`

Decode a percent-encoded URL query component; errors on malformed input.

**Signature:** `encoding.urlDecode(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### `parse_url`

Parse a URL string into {scheme, host, port, path, query, fragment}; errors on malformed input.

**Signature:** `encoding.parseUrl(raw_url) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `raw_url` | `string` |  | |

**Returns:** map[string]any

### `build_url`

Build a URL string from a {scheme, host, port, path, query, fragment} map; missing keys are treated as empty.

**Signature:** `encoding.buildUrl(parts) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `parts` | `map[string]any` |  | |

**Returns:** string

