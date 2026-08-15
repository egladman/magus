---
title: vcs module
aliases: [modules/vcs]
description: Version-control queries for the current working tree.
tags: [vcs, module, stdlib, magusfile]
---

# vcs

Version-control queries for the current working tree.

> **Naming convention:** import the module under its bare name (`import "vcs"`), reach members with a backslash, and call methods in `camelCase`: `vcs\someMethod`.

## Methods

### name

VCS short name (e.g. "git"). Empty if unresolved, which is how a caller tests for a VCS without catching.

**Signature:** `vcs\name() → string` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L181)

**Returns:** string

### base

Resolved base ref for diffs.

**Signature:** `vcs\base() → string` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L190)

**Returns:** string

### root

Absolute path of the repository root.

**Signature:** `vcs\root() → string` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L196)

**Returns:** string

### changedFiles

The files changed against the given base (defaults to vcs.base), each a Path carrying the repository root as its base. Empty when no VCS is resolved. Named for what it returns: it answers WHICH files a branch touched, where vcs.dirtyDiff answers WHAT changed inside the working tree.

**Signature:** `vcs\changedFiles([base]) → [Path]` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L216)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `base` | `string` | yes | |

**Returns:** any

### ref

The movable name pointing at the current revision, or "" when there is none. Backend-specific by nature: a git branch, a Mercurial named branch, a Jujutsu bookmark. jj's working copy is usually an anonymous change, so "" is an ordinary answer there, not a failure. Raises when no VCS is resolved or its metadata cannot be read - use vcs.name() to test for a VCS first.

**Signature:** `vcs\ref() → string` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L285)

**Returns:** string

### status

The working tree's uncommitted state as {clean, files}: clean is true when nothing changed, files are the changed paths (empty when clean). Pass paths to scope it. Each file is a Path carrying the repository root as its base, because a VCS reports paths from the root while a target runs in its project directory. Paths only - a per-entry status code is not portable (jj reports none), so reach for vcs.cmd() when the codes matter.

**Signature:** `vcs\status([paths]) → Status` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L305)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `paths` | `[]string` | yes | |

**Returns:** any

### isDirty

True if the working tree has uncommitted changes. Pass paths to scope the check to those files/dirs (relative to the project), e.g. is_dirty(["MAGUS.md"]) - the right way to gate generated outputs without shelling out to git or parsing porcelain.

**Signature:** `vcs\isDirty([paths]) → bool` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L330)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `paths` | `[]string` | yes | |

**Returns:** bool

### dirtyDiff

The uncommitted changes to paths, as the active VCS's own unified diff; "" when nothing changed or no VCS is resolved. is_dirty answers whether an output moved, this answers how - which is what a drift gate needs when it fires in CI and nobody can look at the tree. Every backend implements it, so a magusfile no longer branches on vcs.name() to print a diff; the bytes are the backend's native format, not a normalized one.

**Signature:** `vcs\dirtyDiff([paths]) → string` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L359)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `paths` | `[]string` | yes | |

**Returns:** string

### commit

Resolve a revision (a VCS-native rev expression; omit for the current revision) to its commit object: {id, short, author {name, email}, date, subject, body, parents}. id is the content/revision id (git SHA, hg node, jj commit_id); date is RFC3339, when the revision was recorded. Every field is meaningful for every VCS. Raises when no VCS is resolved or the revision cannot be looked up, so a caller never has to sniff a field to find out - use vcs.name() to test for a VCS, and try/catch for a revision that may not exist.

**Signature:** `vcs\commit([rev]) → Commit` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L379)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `rev` | `string` | yes | |

**Returns:** any

### history

Up to limit recent commits, newest first; each is the same object vcs.commit returns. limit defaults to 10 when omitted. An empty list when no VCS is resolved.

**Signature:** `vcs\history([limit]) → [Commit]` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L398)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `limit` | `int` | yes | |

**Returns:** any

### cmd

Escape hatch: run the active VCS binary (git/hg/sl/jj) with args, for something no method covers. Same result and raise semantics as magus.cmd and proc.exec - returns {stdout, stderr, code, ok} and raises on a non-zero exit unless opts.allow_failure. opts.dir runs it elsewhere (relative to the target's cwd, unlike proc.exec's positional dir); opts.quiet captures the output without echoing it to the console. This is VCS-AGNOSTIC only in that magus picks the binary; the args are the backend's own, so branch on vcs.name() when they differ. Raises when no VCS is resolved, rather than running nothing and reporting success.

**Signature:** `vcs\cmd(args, [opts]) → ExecResult` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L466)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### tags

Repository tags, newest first. Each is an object {name, date, id}: name as written ("v0.3.0", no refs/tags/ prefix), date RFC3339 (empty when the VCS reported none), id the revision it resolves to. pattern is a glob over the name ("v*"); wildcards stop at "/", so "v*" selects releases and skips a namespaced tag like backup/x. Omit it to list every tag. Empty when no VCS is resolved or the backend has no tags (jj); a failed query raises rather than reporting "no tags". Note a shallow or single-branch clone legitimately fetches no tags, so an empty list still means "none present here", not "none exist".

**Signature:** `vcs\tags([pattern]) → [Tag]` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L428)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `pattern` | `string` | yes | |

**Returns:** any

### describe

Human-readable version string from the nearest tag (git's `describe --tags --always --dirty`: tag, else short hash, with a -dirty suffix for a modified tree). "" when no VCS is resolved, or for a backend without a tag-describe concept (jj) - so a magusfile stamps a version without shelling out to git. Pair with vcs.commit().short as a fallback.

**Signature:** `vcs\describe() → string` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L414)

**Returns:** string

