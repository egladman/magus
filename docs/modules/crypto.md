# `crypto`

Content digests (SHA-256/512; SHA-1 and MD5 for legacy-checksum interop).

> **Naming convention:** Buzz reaches modules off the `import "magus/extra"` aggregate in `camelCase` (`extra.crypto.someMethod`).

## Methods

### `sha256_hex`

Return the lowercase hex SHA-256 digest of data.

**Signature:** `extra.crypto.sha256Hex(data) → string`

**Also in Buzz's stdlib:** `crypto.hash(HashAlgorithm.Sha256, …)` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### `sha256_file`

Return the lowercase hex SHA-256 digest of the file at path.

**Signature:** `extra.crypto.sha256File(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `sha512_hex`

Return the lowercase hex SHA-512 digest of data.

**Signature:** `extra.crypto.sha512Hex(data) → string`

**Also in Buzz's stdlib:** `crypto.hash(HashAlgorithm.Sha512, …)` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### `sha512_file`

Return the lowercase hex SHA-512 digest of the file at path.

**Signature:** `extra.crypto.sha512File(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `sha1_hex`

Return the lowercase hex SHA-1 digest of data. For interop with legacy/git checksums only — SHA-1 is not collision-resistant; use sha256 for anything security-relevant.

**Signature:** `extra.crypto.sha1Hex(data) → string`

**Also in Buzz's stdlib:** `crypto.hash(HashAlgorithm.Sha1, …)` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### `sha1_file`

Return the lowercase hex SHA-1 digest of the file at path. For interop with legacy/git checksums only — SHA-1 is not collision-resistant; use sha256 for anything security-relevant.

**Signature:** `extra.crypto.sha1File(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `md5_hex`

Return the lowercase hex MD5 digest of data. For interop with legacy checksum manifests only — MD5 is broken; use sha256 for anything security-relevant.

**Signature:** `extra.crypto.md5Hex(data) → string`

**Also in Buzz's stdlib:** `crypto.hash(HashAlgorithm.Md5, …)` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### `md5_file`

Return the lowercase hex MD5 digest of the file at path. For interop with legacy checksum manifests only — MD5 is broken; use sha256 for anything security-relevant.

**Signature:** `extra.crypto.md5File(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

