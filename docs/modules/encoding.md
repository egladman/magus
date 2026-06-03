# `encoding`

Base64/hex/URL text codecs.

> **Naming convention:** Buzz reaches modules off the `import "magus/extra"` aggregate in `camelCase` (`extra.encoding.someMethod`).

## Methods

### `base64_encode`

Encode data as standard (padded) base64.

**Signature:** `extra.encoding.base64Encode(data) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### `base64_decode`

Decode a standard (padded) base64 string; errors on malformed input.

**Signature:** `extra.encoding.base64Decode(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### `base64url_encode`

Encode data as URL-safe (padded) base64.

**Signature:** `extra.encoding.base64urlEncode(data) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### `base64url_decode`

Decode a URL-safe (padded) base64 string; errors on malformed input.

**Signature:** `extra.encoding.base64urlDecode(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### `hex_encode`

Encode data as lowercase hex.

**Signature:** `extra.encoding.hexEncode(data) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### `hex_decode`

Decode a hex string; errors on malformed input.

**Signature:** `extra.encoding.hexDecode(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### `url_encode`

Percent-encode s for use in a URL query component.

**Signature:** `extra.encoding.urlEncode(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### `url_decode`

Decode a percent-encoded URL query component; errors on malformed input.

**Signature:** `extra.encoding.urlDecode(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

