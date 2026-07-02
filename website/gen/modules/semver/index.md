---
title: semver module
description: Semantic version parsing and comparison per SemVer 2.0.0. Parse into structured parts and compare two versions with relational operators.
tags: [semver, version, parse, compare, semantic versioning, magus stdlib]
---

# semver

Semantic version parsing and comparison (SemVer 2.0.0).

> **Naming convention:** import the module under its bare name (`import "semver"`) and call methods in `camelCase` (`semver.someMethod`).

## Methods

### compare

Compare two semver strings; op is "==", "!=", "<", "<=", ">", or ">=" — true when the relation holds.

**Signature:** `semver.compare(a, op, b) → bool` · [source](https://github.com/egladman/magus/blob/main/std/semver.go#L43)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `a` | `string` |  | |
| `op` | `string` |  | |
| `b` | `string` |  | |

**Returns:** bool

### parse

Parse a semver string into {major, minor, patch, prerelease, metadata, original}; errors on invalid input.

**Signature:** `semver.parse(v) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/semver.go#L56)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `v` | `string` |  | |

**Returns:** map[string]any

