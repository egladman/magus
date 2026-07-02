---
title: path module
description: "Pure path-string math: abs, rel, clean, is_abs, expand_user."
tags: [path, module, stdlib, magusfile]
---

# path

Pure path-string math: abs, rel, clean, is_abs, expand_user.

> **Naming convention:** import the module under its bare name (`import "path"`) and call methods in `camelCase` (`path.someMethod`).

## Methods

### abs

Return the absolute form of path, resolved against the current directory and lexically cleaned.

**Signature:** `path.abs(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L64)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### rel

Return a relative path from base to target; errors if no relative path exists.

**Signature:** `path.rel(base, target) → string` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L73)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `base` | `string` |  | |
| `target` | `string` |  | |

**Returns:** string

### clean

Return the shortest lexically-equivalent path (resolves . and .., collapses separators).

**Signature:** `path.clean(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L82)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### is_abs

Report whether path is absolute.

**Signature:** `path.isAbs(path) → bool` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L87)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

### expand_user

Expand a leading ~ (or ~/...) to the current user's home directory; other paths are returned unchanged.

**Signature:** `path.expandUser(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L94)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

