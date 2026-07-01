# encoding

Base64/hex/URL text codecs.

> **Naming convention:** import the module under its bare name (`import "encoding"`) and call methods in `camelCase` (`encoding.someMethod`).

## Methods

### base64_encode

Encode data as standard (padded) base64.

**Signature:** `encoding.base64Encode(data) → string`[^buzz-stdlib-encoding-base64_encode] · [source](https://github.com/egladman/magus/blob/main/std/encoding.go#L102)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### base64_decode

Decode a standard (padded) base64 string; errors on malformed input.

**Signature:** `encoding.base64Decode(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding.go#L107)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### base64url_encode

Encode data as URL-safe (padded) base64.

**Signature:** `encoding.base64urlEncode(data) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding.go#L116)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### base64url_decode

Decode a URL-safe (padded) base64 string; errors on malformed input.

**Signature:** `encoding.base64urlDecode(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding.go#L121)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### hex_encode

Encode data as lowercase hex.

**Signature:** `encoding.hexEncode(data) → string`[^buzz-stdlib-encoding-hex_encode] · [source](https://github.com/egladman/magus/blob/main/std/encoding.go#L130)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### hex_decode

Decode a hex string; errors on malformed input.

**Signature:** `encoding.hexDecode(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding.go#L135)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### url_encode

Percent-encode s for use in a URL query component.

**Signature:** `encoding.urlEncode(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding.go#L144)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### url_decode

Decode a percent-encoded URL query component; errors on malformed input.

**Signature:** `encoding.urlDecode(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding.go#L149)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### parse_url

Parse a URL string into {scheme, host, port, path, query, fragment}; errors on malformed input.

**Signature:** `encoding.parseUrl(raw_url) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/encoding.go#L158)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `raw_url` | `string` |  | |

**Returns:** map[string]any

### build_url

Build a URL string from a {scheme, host, port, path, query, fragment} map; missing keys are treated as empty.

**Signature:** `encoding.buildUrl(parts) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding.go#L175)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `parts` | `map[string]any` |  | |

**Returns:** string

[^buzz-stdlib-encoding-base64_encode]: `encoding.base64Encode` is also in Buzz's standard library (`str.encodeBase64 (built-in string method)`); the magus form is sandbox-aware.
[^buzz-stdlib-encoding-hex_encode]: `encoding.hexEncode` is also in Buzz's standard library (`str.hex (built-in string method)`); the magus form is sandbox-aware.
