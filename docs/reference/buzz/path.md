---
title: path module
aliases: [modules/path]
description: "Pure path-string math: abs, rel, clean, is_abs, expand_user."
tags: [path, module, stdlib, magusfile]
---

# path

Pure path-string math: abs, rel, clean, is_abs, expand_user.

> **Naming convention:** import the module under its bare name (`import "path"`), reach members with a backslash, and call methods in `camelCase`: `path\someMethod`.

## Methods

### abs

Return the absolute form of path, resolved against the current directory and lexically cleaned.

**Signature:** `path\abs(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L67)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### rel

Return a relative path from base to target; errors if no relative path exists.

**Signature:** `path\rel(base, target) → string` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L76)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `base` | `string` |  | |
| `target` | `string` |  | |

**Returns:** string

### clean

Return the shortest lexically-equivalent path (resolves . and .., collapses separators).

**Signature:** `path\clean(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L85)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### isAbs

Report whether path is absolute.

**Signature:** `path\isAbs(path) → bool` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L90)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

### expandUser

Expand a leading ~ (or ~/...) to the current user's home directory; other paths are returned unchanged.

**Signature:** `path\expandUser(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L97)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

