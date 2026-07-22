---
title: crypto module
aliases: [modules/crypto]
description: Content digests (SHA-256/512; SHA-1 and MD5 for legacy-checksum interop).
tags: [crypto, module, stdlib, magusfile]
---

# crypto

Content digests (SHA-256/512; SHA-1 and MD5 for legacy-checksum interop).

> **Naming convention:** import the module under its bare name (`import "crypto"`) and call methods in `camelCase` (`crypto.someMethod`).

## Methods

### sha256_hex

Return the lowercase hex SHA-256 digest of data.

**Signature:** `crypto.sha256Hex(data) → string`[^buzz-stdlib-crypto-sha256_hex] · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L121)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### sha256_file

Return the lowercase hex SHA-256 digest of the file at path.

**Signature:** `crypto.sha256File(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L126)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### sha512_hex

Return the lowercase hex SHA-512 digest of data.

**Signature:** `crypto.sha512Hex(data) → string`[^buzz-stdlib-crypto-sha512_hex] · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L131)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### sha512_file

Return the lowercase hex SHA-512 digest of the file at path.

**Signature:** `crypto.sha512File(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L136)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### sha1_hex

Return the lowercase hex SHA-1 digest of data. For interop with legacy/git checksums only - SHA-1 is not collision-resistant; use sha256 for anything security-relevant.

**Signature:** `crypto.sha1Hex(data) → string`[^buzz-stdlib-crypto-sha1_hex] · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L141)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### sha1_file

Return the lowercase hex SHA-1 digest of the file at path. For interop with legacy/git checksums only - SHA-1 is not collision-resistant; use sha256 for anything security-relevant.

**Signature:** `crypto.sha1File(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L146)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### md5_hex

Return the lowercase hex MD5 digest of data. For interop with legacy checksum manifests only - MD5 is broken; use sha256 for anything security-relevant.

**Signature:** `crypto.md5Hex(data) → string`[^buzz-stdlib-crypto-md5_hex] · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L151)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### md5_file

Return the lowercase hex MD5 digest of the file at path. For interop with legacy checksum manifests only - MD5 is broken; use sha256 for anything security-relevant.

**Signature:** `crypto.md5File(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L156)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

[^buzz-stdlib-crypto-sha256_hex]: `crypto.sha256Hex` is also in Buzz's standard library (`crypto.hash(HashAlgorithm.Sha256, …)`); the magus form is sandbox-aware.
[^buzz-stdlib-crypto-sha512_hex]: `crypto.sha512Hex` is also in Buzz's standard library (`crypto.hash(HashAlgorithm.Sha512, …)`); the magus form is sandbox-aware.
[^buzz-stdlib-crypto-sha1_hex]: `crypto.sha1Hex` is also in Buzz's standard library (`crypto.hash(HashAlgorithm.Sha1, …)`); the magus form is sandbox-aware.
[^buzz-stdlib-crypto-md5_hex]: `crypto.md5Hex` is also in Buzz's standard library (`crypto.hash(HashAlgorithm.Md5, …)`); the magus form is sandbox-aware.
