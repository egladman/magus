---
title: crypto module
aliases: [modules/crypto]
description: Content digests (SHA-256/512; SHA-1 and MD5 for legacy-checksum interop) and Ed25519 signing.
tags: [crypto, module, stdlib, magusfile]
---

# crypto

Content digests (SHA-256/512; SHA-1 and MD5 for legacy-checksum interop) and Ed25519 signing.

> **Naming convention:** import the module under its bare name (`import "crypto"`), reach members with a backslash, and call methods in `camelCase`: `crypto\someMethod`.

## Methods

### sha256Hex

Return the lowercase hex SHA-256 digest of data.

**Signature:** `crypto\sha256Hex(data) → string`[^buzz-stdlib-crypto-sha256_hex] · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L165)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### sha256File

Return the lowercase hex SHA-256 digest of the file at path.

**Signature:** `crypto\sha256File(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L170)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### sha512Hex

Return the lowercase hex SHA-512 digest of data.

**Signature:** `crypto\sha512Hex(data) → string`[^buzz-stdlib-crypto-sha512_hex] · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L175)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### sha512File

Return the lowercase hex SHA-512 digest of the file at path.

**Signature:** `crypto\sha512File(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L180)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### sha1Hex

Return the lowercase hex SHA-1 digest of data. For interop with legacy/git checksums only - SHA-1 is not collision-resistant; use sha256 for anything security-relevant.

**Signature:** `crypto\sha1Hex(data) → string`[^buzz-stdlib-crypto-sha1_hex] · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L185)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### sha1File

Return the lowercase hex SHA-1 digest of the file at path. For interop with legacy/git checksums only - SHA-1 is not collision-resistant; use sha256 for anything security-relevant.

**Signature:** `crypto\sha1File(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L190)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### sign

Sign data with the private key in the named environment variable and return the lowercase hex signature. alg is "ed25519". The key is NAMED, never passed: a value that never enters Buzz cannot be interpolated into a log.

**Signature:** `crypto\sign(alg, data, key_env) → string` · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L248)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `alg` | `string` |  | |
| `data` | `string` |  | |
| `key_env` | `string` |  | |

**Returns:** string

### signFile

Sign the file at path, write the detached signature to path + ".sig", and return the lowercase hex signature. alg is "ed25519".

**Signature:** `crypto\signFile(alg, path, key_env) → string` · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L261)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `alg` | `string` |  | |
| `path` | `string` |  | |
| `key_env` | `string` |  | |

**Returns:** string

### verify

Report whether sig_hex is a valid signature over data for the hex public key pub_hex. alg is "ed25519".

**Signature:** `crypto\verify(alg, data, sig_hex, pub_hex) → bool` · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L291)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `alg` | `string` |  | |
| `data` | `string` |  | |
| `sig_hex` | `string` |  | |
| `pub_hex` | `string` |  | |

**Returns:** bool

### publicKey

Return the lowercase hex PUBLIC key for the private key in the named environment variable, so a publisher can print what its readers must pin. alg is "ed25519".

**Signature:** `crypto\publicKey(alg, key_env) → string` · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L312)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `alg` | `string` |  | |
| `key_env` | `string` |  | |

**Returns:** string

### md5Hex

Return the lowercase hex MD5 digest of data. For interop with legacy checksum manifests only - MD5 is broken; use sha256 for anything security-relevant.

**Signature:** `crypto\md5Hex(data) → string`[^buzz-stdlib-crypto-md5_hex] · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L195)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `data` | `string` |  | |

**Returns:** string

### md5File

Return the lowercase hex MD5 digest of the file at path. For interop with legacy checksum manifests only - MD5 is broken; use sha256 for anything security-relevant.

**Signature:** `crypto\md5File(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/crypto.go#L200)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

[^buzz-stdlib-crypto-sha256_hex]: `crypto\sha256Hex` is also in Buzz's standard library (`crypto.hash(HashAlgorithm.Sha256, …)`); the magus form is sandbox-aware.
[^buzz-stdlib-crypto-sha512_hex]: `crypto\sha512Hex` is also in Buzz's standard library (`crypto.hash(HashAlgorithm.Sha512, …)`); the magus form is sandbox-aware.
[^buzz-stdlib-crypto-sha1_hex]: `crypto\sha1Hex` is also in Buzz's standard library (`crypto.hash(HashAlgorithm.Sha1, …)`); the magus form is sandbox-aware.
[^buzz-stdlib-crypto-md5_hex]: `crypto\md5Hex` is also in Buzz's standard library (`crypto.hash(HashAlgorithm.Md5, …)`); the magus form is sandbox-aware.
