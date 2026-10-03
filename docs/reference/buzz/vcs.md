---
title: vcs module
generated_from: reference/buzz/
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

**Signature:** `vcs\name() -> string` - [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L237)

**Returns:** string

### base

Resolved base ref for diffs. dir resolves it for the repository holding that directory (relative to the target's cwd) instead of the one holding the cwd, so it names that repository's VCS default. Raises only when dir does not exist.

**Signature:** `vcs\base([dir]) -> string` - [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L247)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `dir`     | `string` | yes      |             |

**Returns:** string

### root

Absolute path of the repository root.

**Signature:** `vcs\root() -> string` - [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L253)

**Returns:** string

### changedFiles

The files changed against the given base (defaults to the base vcs\base resolves for dir), each a Path carrying the repository root as its base. dir reads the repository holding that directory (relative to the target's cwd) instead of the one holding the cwd; a dir that does not exist raises. Empty when no VCS is resolved. Named for what it returns: it answers WHICH files a branch touched, where vcs\dirtyDiff answers WHAT changed inside the working tree.

**Signature:** `vcs\changedFiles([base], [dir]) -> [Path]` - [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L274)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `base`    | `string` | yes      |             |
| `dir`     | `string` | yes      |             |

**Returns:** any

### regions

The declarations the change against base (defaults to vcs\base) lands in: one {file, side, lines, declaration, driver} per declaration each hunk touches, ordered by path, then side, then line, for the same files vcs\changedFiles lists, and dir reads the repository holding that directory as it does there. side is `old` for lines only the merge base's version has (a deletion) and `new` for the working tree's; lines is the first and last line on that side, 1-based and inclusive; declaration is the enclosing declaration's line as the file's diff driver matched it (`func (m *Magus) run(ctx context.Context) error {`), empty above a file's first declaration; driver is that diff driver (`golang`, `markdown`, `buzz`), empty for a file with none, whose regions then say only which lines changed. It is the footprint `magus job wait` prints. Empty when no VCS is resolved; raises when the backend cannot place regions (only git can) or the diff cannot be computed, since an empty footprint reads as a change that touched nothing.

**Signature:** `vcs\regions([base], [dir]) -> [RegionChange]` - [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L311)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `base`    | `string` | yes      |             |
| `dir`     | `string` | yes      |             |

**Returns:** any

### ref

The movable name pointing at the current revision, or null when none names it: a detached git HEAD, or jj's working copy, which is usually an anonymous change, so null is an ordinary answer there, not a failure. Backend-specific by nature: a git branch, a Mercurial named branch, a Jujutsu bookmark. dir reads the repository holding that directory (relative to the target's cwd) instead of the one holding the cwd. Raises when no VCS is resolved, its metadata cannot be read, or dir does not exist - use vcs\name() to test for a VCS first.

**Signature:** `vcs\ref([dir]) -> string?` - [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L362)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `dir`     | `string` | yes      |             |

**Returns:** string or null

### status

The working tree's uncommitted state as {clean, files}: clean is true when nothing changed, files are the changed paths (empty when clean). Pass paths to scope it. Each file is a Path carrying the repository root as its base, because a VCS reports paths from the root while a target runs in its project directory. Paths only - a per-entry status code is not portable (jj reports none), so reach for vcs\cmd() when the codes matter.

**Signature:** `vcs\status([paths]) -> Status` - [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L393)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `paths`   | `[]string` | yes      |             |

**Returns:** any

### isDirty

True if the working tree has uncommitted changes. Pass paths to scope the check to those files/dirs (relative to the project), e.g. is_dirty(["MAGUS.md"]) - the right way to gate generated outputs without shelling out to git or parsing porcelain.

**Signature:** `vcs\isDirty([paths]) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L418)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `paths`   | `[]string` | yes      |             |

**Returns:** bool

### dirtyDiff

The uncommitted changes to paths, as the active VCS's own unified diff; "" when nothing changed or no VCS is resolved. is_dirty answers whether an output moved, this answers how - which is what a drift gate needs when it fires in CI and nobody can look at the tree. Every backend implements it, so a magusfile no longer branches on vcs\name() to print a diff; the bytes are the backend's native format, not a normalized one.

**Signature:** `vcs\dirtyDiff([paths]) -> string` - [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L447)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `paths`   | `[]string` | yes      |             |

**Returns:** string

### commit

Resolve a revision (a VCS-native rev expression; omit for the current revision) to its commit object: {id, short, author {name, email}, date, subject, body, parents, files}. id is the content/revision id (git SHA, hg node, jj commit_id); date is RFC3339 in the committer's own offset, when the revision was recorded. files stays empty here, meaning not asked; vcs\history fills it. Every field is meaningful for every VCS. Raises when no VCS is resolved or the revision cannot be looked up, so a caller never has to sniff a field to find out - use vcs\name() to test for a VCS, and try/catch for a revision that may not exist.

**Signature:** `vcs\commit([rev]) -> Commit` - [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L467)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `rev`     | `string` | yes      |             |

**Returns:** any

### history

Up to limit commits reachable from the current revision, newest first, in one VCS call however many there are; each is the object vcs\commit returns, with files set to the repository-relative paths it changed against its first parent (a rename is both paths). limit defaults to 10; 0 means every commit. paths keeps only the commits that changed one of those literal repository-relative paths (a directory keeps what is under it) and narrows each commit's files to them. first_parent follows only the first parent of a merge, the line a branch landed on. An empty list when no VCS is resolved.

**Signature:** `vcs\history([limit], [paths], [first_parent]) -> [Commit]` - [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L486)

| Parameter      | Type       | Optional | Description |
| -------------- | ---------- | -------- | ----------- |
| `limit`        | `int`      | yes      |             |
| `paths`        | `[]string` | yes      |             |
| `first_parent` | `bool`     | yes      |             |

**Returns:** any

### cmd

Escape hatch: run the active VCS binary (git/hg/sl/jj) with args, for something no method covers. Same result and raise semantics as magus\cmd and proc\exec - returns {stdout, stderr, code, ok} and raises on a non-zero exit unless opts.allow_failure. opts.dir runs it elsewhere (relative to the target's cwd, unlike proc\exec's positional dir); opts.quiet captures the output without echoing it to the console. This is VCS-AGNOSTIC only in that magus picks the binary; the args are the backend's own, so branch on vcs\name() when they differ. Raises when no VCS is resolved, rather than running nothing and reporting success.

**Signature:** `vcs\cmd(args, [opts]) -> ExecResult` - [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L555)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `args`    | `[]string`       |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### tags

Repository tags, newest first. Each is an object {name, date, id}: name as written ("v0.3.0", no refs/tags/ prefix), date RFC3339 (empty when the VCS reported none), id the revision it resolves to. pattern is a glob over the name ("v*"); wildcards stop at "/", so "v*" selects releases and skips a namespaced tag like backup/x. Omit it to list every tag. Empty when no VCS is resolved or the backend has no tags (jj); a failed query raises rather than reporting "no tags". Note a shallow or single-branch clone legitimately fetches no tags, so an empty list still means "none present here", not "none exist".

**Signature:** `vcs\tags([pattern]) -> [Tag]` - [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L517)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `pattern` | `string` | yes      |             |

**Returns:** any

### describe

Human-readable version string from the nearest tag (git's `describe --tags --always --dirty`: tag, else short hash, with a -dirty suffix for a modified tree). "" when no VCS is resolved, or for a backend without a tag-describe concept (jj) - so a magusfile stamps a version without shelling out to git. Pair with vcs\commit().short as a fallback.

**Signature:** `vcs\describe() -> string` - [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L503)

**Returns:** string

