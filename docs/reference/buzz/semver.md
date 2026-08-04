---
title: semver module
aliases: [modules/semver]
description: Semantic version parsing and comparison (SemVer 2.0.0).
tags: [semver, module, stdlib, magusfile]
---

# semver

Semantic version parsing and comparison (SemVer 2.0.0).

> **Naming convention:** import the module under its bare name (`import "semver"`), reach members with a backslash, and call methods in `camelCase`: `semver\someMethod`.

## Methods

### compare

Compare two semver strings; op is "==", "!=", "<", "<=", ">", or ">=" - true when the relation holds.

**Signature:** `semver\compare(a, op, b) → bool` · [source](https://github.com/egladman/magus/blob/main/std/semver.go#L50)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `a` | `string` |  | |
| `op` | `string` |  | |
| `b` | `string` |  | |

**Returns:** bool

### parse

Parse a semver string into {major, minor, patch, prerelease, metadata, original}; errors on invalid input.

**Signature:** `semver\parse(v) → SemverVersion` · [source](https://github.com/egladman/magus/blob/main/std/semver.go#L63)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `v` | `string` |  | |

**Returns:** map[string]any

### next

Candidate next versions after v: {major, minor, patch}, each "vX.Y.Z" - the result of bumping the major, minor, or patch component. Errors on invalid input.

**Signature:** `semver\next(v) → SemverNext` · [source](https://github.com/egladman/magus/blob/main/std/semver.go#L84)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `v` | `string` |  | |

**Returns:** map[string]any

## See also

- [Standard library modules](index.md)
