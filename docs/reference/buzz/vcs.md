---
title: vcs module
aliases: [modules/vcs]
description: Version-control queries for the current working tree.
tags: [vcs, module, stdlib, magusfile]
---

# vcs

Version-control queries for the current working tree.

> **Naming convention:** import the module under its bare name (`import "vcs"`), reach members with a backslash, and call methods in `camelCase`: `vcs\someMethod`.

## Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | `string` | VCS short name (e.g. "git"). Empty if unresolved. |
| `base` | `string` | Resolved base ref for diffs. |

## Methods

### root

Absolute path of the repository root.

**Signature:** `vcs\root() → string` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L195)

**Returns:** string

### diff

List files changed against the given base (defaults to vcs.base).

**Signature:** `vcs\diff([base]) → []string` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L208)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `base` | `string` | yes | |

**Returns:** []string

### shortHash

Short commit hash. Raises when no VCS is resolved or its metadata cannot be read - use vcs.name() to test for a VCS first.

**Signature:** `vcs\shortHash() → string` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L248)

**Returns:** string

### hash

Full commit hash. Raises when no VCS is resolved or its metadata cannot be read - use vcs.name() to test for a VCS first.

**Signature:** `vcs\hash() → string` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L257)

**Returns:** string

### branch

Current branch. Raises when no VCS is resolved or its metadata cannot be read - use vcs.name() to test for a VCS first.

**Signature:** `vcs\branch() → string` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L266)

**Returns:** string

### commitDate

Commit date string. Raises when no VCS is resolved or its metadata cannot be read - use vcs.name() to test for a VCS first.

**Signature:** `vcs\commitDate() → string` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L275)

**Returns:** string

### isDirty

True if the working tree has uncommitted changes. Pass paths to scope the check to those files/dirs (relative to the project), e.g. is_dirty(["MAGUS.md"]) - the right way to gate generated outputs without shelling out to git or parsing porcelain.

**Signature:** `vcs\isDirty([paths]) → bool` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L388)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `paths` | `[]string` | yes | |

**Returns:** bool

### dirtyFiles

The changed entries in the working tree as the backend's own status lines (git porcelain, hg status, jj diff --name-only), or an empty list when clean. Pass paths to scope it, exactly like is_dirty. This is is_dirty with the detail: a gate that has to say WHICH generated files moved asks this instead of shelling out to the VCS and parsing it by hand.

**Signature:** `vcs\dirtyFiles([paths]) → []string` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L371)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `paths` | `[]string` | yes | |

**Returns:** []string

### diagnoseDrift

Diagnose why a generate gate's outputs drifted and RETURN the verdict {drifted, code, message, url, files} so the caller decides whether to fail or warn. Pass the target's output globs and (optional) input globs, project-relative. code is MGS4006 when a declared input changed (real drift, commit it), MGS4005 when the inputs are unchanged but a dev build produced differing output (version/tool skew, not your change), or MGS4003 when a release build's identical inputs still differ (a reproducibility bug); files lists the drifted outputs as the backend's status lines, so a gate reports WHICH files moved without shelling out to the VCS. drifted is false with empty fields when the outputs are clean. Composes is_dirty; does not replace it.

**Signature:** `vcs\diagnoseDrift(outputs, [inputs]) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L308)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `outputs` | `[]string` |  | |
| `inputs` | `[]string` | yes | |

**Returns:** map[string]any

### metadata

Full metadata table: short_hash, hash, branch, commit_date, is_dirty.

**Signature:** `vcs\metadata() → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L414)

**Returns:** map[string]any

### commit

Resolve a revision (a VCS-native rev expression; omit for the current revision) to its commit object: {id, short, author {name, email}, date, subject, body, parents}. id is the content/revision id (git SHA, hg node, jj commit_id); date is RFC3339, when the revision was recorded. Every field is meaningful for every VCS. Raises when no VCS is resolved or the revision cannot be looked up, so a caller never has to sniff a field to find out - use vcs.name() to test for a VCS, and try/catch for a revision that may not exist.

**Signature:** `vcs\commit([rev]) → Commit` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L442)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `rev` | `string` | yes | |

**Returns:** any

### history

Up to limit recent commits, newest first; each is the same object vcs.commit returns. limit defaults to 10 when omitted. An empty list when no VCS is resolved.

**Signature:** `vcs\history([limit]) → [Commit]` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L460)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `limit` | `int` | yes | |

**Returns:** any

### exe

Absolute path to the active VCS executable (git/hg/jj), or "" if unresolved. Lets a magusfile run a VCS-agnostic escape-hatch command: os.exec(vcs.exe(), [...]).

**Signature:** `vcs\exe() → string` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L507)

**Returns:** string

### tags

Repository tags, newest first. Each is an object {name, date, id}: name as written ("v0.3.0", no refs/tags/ prefix), date RFC3339 (empty when the VCS reported none), id the revision it resolves to. pattern is a glob over the name ("v*"); wildcards stop at "/", so "v*" selects releases and skips a namespaced tag like backup/x. Omit it to list every tag. Empty when no VCS is resolved or the backend has no tags (jj); a failed query raises rather than reporting "no tags". Note a shallow or single-branch clone legitimately fetches no tags, so an empty list still means "none present here", not "none exist".

**Signature:** `vcs\tags([pattern]) → [Tag]` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L490)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `pattern` | `string` | yes | |

**Returns:** any

### describe

Human-readable version string from the nearest tag (git's `describe --tags --always --dirty`: tag, else short hash, with a -dirty suffix for a modified tree). "" when no VCS is resolved, or for a backend without a tag-describe concept (jj) - so a magusfile stamps a version without shelling out to git. Pair with vcs.shortHash() as a fallback.

**Signature:** `vcs\describe() → string` · [source](https://github.com/egladman/magus/blob/main/std/vcs.go#L476)

**Returns:** string

