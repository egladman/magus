---
title: sort module
generated_from: reference/buzz/
aliases: [modules/sort]
description: "Ordering for string lists: lexicographic, natural (digit-aware), and semver."
tags: [sort, module, stdlib, magusfile]
---

# sort

Ordering for string lists: lexicographic, natural (digit-aware), and semver.

> **Naming convention:** import the module under its bare name (`import "sort"`), reach members with a backslash, and call methods in `camelCase`: `sort\someMethod`.

## Methods

### strings

Return a new list ordered lexicographically by byte. The plain alphabetical sort, and the one to reach for when the goal is a stable order rather than a meaningful one - a listing that must not change between runs.

**Signature:** `sort\strings(items) -> []string` - [source](https://github.com/egladman/magus/blob/main/std/sort.go#L58)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `items` | `[]string` |  | |

**Returns:** []string

### natural

Return a new list ordered so embedded numbers compare as numbers: file2 before file10, where a lexicographic sort puts file10 first. Use it for anything a human will read - filenames, shard names, versioned directories - and note it is NOT a version sort; semver is.

**Signature:** `sort\natural(items) -> []string` - [source](https://github.com/egladman/magus/blob/main/std/sort.go#L65)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `items` | `[]string` |  | |

**Returns:** []string

### semver

Return a new list ordered by semantic version, oldest first, so v1.9.0 precedes v1.10.0 and a prerelease precedes its release. Accepts tags with or without a leading v. Anything that is not a valid version sorts AFTER every valid one, in lexicographic order among themselves - so a stray tag is visible at the end rather than silently reordering the releases.

**Signature:** `sort\semver(items) -> []string` - [source](https://github.com/egladman/magus/blob/main/std/sort.go#L124)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `items` | `[]string` |  | |

**Returns:** []string

