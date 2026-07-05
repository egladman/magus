---
title: uuid module
aliases: [modules/uuid]
description: Unique identifiers and random tokens (v4 random, v7 time-ordered, plus raw random hex/tokens).
tags: [uuid, module, stdlib, magusfile]
---

# uuid

Unique identifiers and random tokens (v4 random, v7 time-ordered, plus raw random hex/tokens).

> **Naming convention:** import the module under its bare name (`import "uuid"`) and call methods in `camelCase` (`uuid.someMethod`).

## Methods

### v4

A random (version 4) UUID string, e.g. "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d".

**Signature:** `uuid.v4() → string` · [source](https://github.com/egladman/magus/blob/main/std/uuid.go#L58)

**Returns:** string

### v7

A time-ordered (version 7) UUID string; lexically sorts by creation time, which makes it a good ordered run/build id.

**Signature:** `uuid.v7() → string` · [source](https://github.com/egladman/magus/blob/main/std/uuid.go#L67)

**Returns:** string

### randomHex

A cryptographically random lowercase hex string of n bytes (2*n characters); errors when n is not positive.

**Signature:** `uuid.randomHex(n) → string` · [source](https://github.com/egladman/magus/blob/main/std/uuid.go#L76)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `n` | `int` |  | |

**Returns:** string

### randomToken

A cryptographically random URL-safe base64 token from n bytes of entropy (no padding); errors when n is not positive.

**Signature:** `uuid.randomToken(n) → string` · [source](https://github.com/egladman/magus/blob/main/std/uuid.go#L88)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `n` | `int` |  | |

**Returns:** string

