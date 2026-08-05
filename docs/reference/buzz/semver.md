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

**Signature:** `semver\compare(a, op, b) → bool` · [source](https://github.com/egladman/magus/blob/main/std/semver.go#L88)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `a` | `string` |  | |
| `op` | `string` |  | |
| `b` | `string` |  | |

**Returns:** bool

### isValid

Whether v parses as a semantic version. Use it instead of calling parse purely to see whether it raises.

**Signature:** `semver\isValid(v) → bool` · [source](https://github.com/egladman/magus/blob/main/std/semver.go#L142)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `v` | `string` |  | |

**Returns:** bool

### canonical

Canonical "vX.Y.Z" form of v, filling in missing components and discarding build metadata; errors on invalid input.

**Signature:** `semver\canonical(v) → string` · [source](https://github.com/egladman/magus/blob/main/std/semver.go#L153)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `v` | `string` |  | |

**Returns:** string

### major

The major prefix of v as a string: major("1.2.3") is "v1". This is the cache token a spell's VersionKey{upTo = "major"} produces; parse().major is the same number as an int.

**Signature:** `semver\major(v) → string` · [source](https://github.com/egladman/magus/blob/main/std/semver.go#L167)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `v` | `string` |  | |

**Returns:** string

### majorMinor

The major.minor prefix of v as a string: majorMinor("1.2.3") is "v1.2". This is the cache token a spell's VersionKey{upTo = "minor"} produces.

**Signature:** `semver\majorMinor(v) → string` · [source](https://github.com/egladman/magus/blob/main/std/semver.go#L177)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `v` | `string` |  | |

**Returns:** string

### satisfies

Whether v meets constraint, the full range syntax magus.yaml required_version uses: ">= 1.2, < 2.0", "^1.2.3", "~1.2". compare() tests one relation; this tests a range.

**Signature:** `semver\satisfies(v, constraint) → bool` · [source](https://github.com/egladman/magus/blob/main/std/semver.go#L190)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `v` | `string` |  | |
| `constraint` | `string` |  | |

**Returns:** bool

### parse

Parse a semver string into {major, minor, patch, prerelease, metadata, original}; errors on invalid input.

**Signature:** `semver\parse(v) → SemverVersion` · [source](https://github.com/egladman/magus/blob/main/std/semver.go#L101)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `v` | `string` |  | |

**Returns:** map[string]any

### next

Candidate next versions after v: {major, minor, patch}, each "vX.Y.Z" - the result of bumping the major, minor, or patch component. Errors on invalid input.

**Signature:** `semver\next(v) → SemverNext` · [source](https://github.com/egladman/magus/blob/main/std/semver.go#L122)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `v` | `string` |  | |

**Returns:** map[string]any

