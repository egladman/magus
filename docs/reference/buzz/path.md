---
title: path module
aliases: [modules/path]
description: "Pure path-string math: abs, rel, clean, is_abs, expand_user, and glob matching."
tags: [path, module, stdlib, magusfile]
---

# path

Pure path-string math: abs, rel, clean, is_abs, expand_user, and glob matching.

> **Naming convention:** import the module under its bare name (`import "path"`), reach members with a backslash, and call methods in `camelCase`: `path\someMethod`.

## Methods

### abs

Return the absolute form of path, resolved against the current directory and lexically cleaned.

**Signature:** `path\abs(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L94)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### rel

Return a relative path from base to target; errors if no relative path exists.

**Signature:** `path\rel(base, target) → string` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L103)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `base` | `string` |  | |
| `target` | `string` |  | |

**Returns:** string

### clean

Return the shortest lexically-equivalent path (resolves . and .., collapses separators).

**Signature:** `path\clean(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L112)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### isAbs

Report whether path is absolute.

**Signature:** `path\isAbs(path) → bool` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L144)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

### matches

Report whether path matches a doublestar glob (** crosses directory separators, * does not). Purely lexical: unlike fs.glob it touches no filesystem, so it is what filters a list already in hand - the changed files from vcs.changed_files, the entries from archive.list - against the same pattern syntax a target's sources use. Raises on a malformed pattern rather than reporting no match, so a typo is not read as "nothing changed".

**Signature:** `path\matches(pattern, path) → bool` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L121)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `pattern` | `string` |  | |
| `path` | `string` |  | |

**Returns:** bool

### matchesAny

Report whether path matches ANY of the patterns; an empty pattern list is false. The common shape of a declared source or ignore set, which is a list rather than one glob.

**Signature:** `path\matchesAny(patterns, path) → bool` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L130)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `patterns` | `[]string` |  | |
| `path` | `string` |  | |

**Returns:** bool

### expandUser

Expand a leading ~ (or ~/...) to the current user's home directory; other paths are returned unchanged.

**Signature:** `path\expandUser(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/path.go#L151)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

