# `vcs`

Version-control queries for the current working tree.

> **Naming convention:** Buzz reaches modules off the `import "magus/extra"` aggregate in `camelCase` (`extra.vcs.someMethod`).

## Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | `string` | VCS short name (e.g. "git"). Empty if unresolved. |
| `base` | `string` | Resolved base ref for diffs. |

## Methods

### `root`

Absolute path of the repository root.

**Signature:** `extra.vcs.root() → string`

**Returns:** string

### `diff`

List files changed against the given base (defaults to vcs.base).

**Signature:** `extra.vcs.diff([base]) → []string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `base` | `string` | yes | |

**Returns:** []string

### `short_hash`

Short commit hash, or empty on error.

**Signature:** `extra.vcs.shortHash() → string`

**Returns:** string

### `hash`

Full commit hash, or empty on error.

**Signature:** `extra.vcs.hash() → string`

**Returns:** string

### `branch`

Current branch, or empty on error.

**Signature:** `extra.vcs.branch() → string`

**Returns:** string

### `commit_date`

Commit date string, or empty on error.

**Signature:** `extra.vcs.commitDate() → string`

**Returns:** string

### `is_dirty`

True if the working tree has uncommitted changes.

**Signature:** `extra.vcs.isDirty() → bool`

**Returns:** bool

### `metadata`

Full metadata table: short_hash, hash, branch, commit_date, is_dirty.

**Signature:** `extra.vcs.metadata() → map[string]any`

**Returns:** map[string]any

### `commit`

Resolve a revision (a VCS-native rev expression; omit for the current revision) to its commit record: {id, short, author {name, email}, date, subject, body, parents}. id is the content/revision id (git SHA, hg node, jj commit_id); date is RFC3339, when the revision was recorded. Every field is meaningful for every VCS. Returns nil when no VCS is resolved or the revision can't be looked up.

**Signature:** `extra.vcs.commit([rev]) → any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `rev` | `string` | yes | |

**Returns:** any

### `history`

Up to limit recent commits, newest first; each is the same record vcs.commit returns. An empty list when no VCS is resolved.

**Signature:** `extra.vcs.history(limit) → any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `limit` | `int` |  | |

**Returns:** any

### `exe`

Absolute path to the active VCS executable (git/hg/jj), or "" if unresolved. Lets a magusfile run a VCS-agnostic escape-hatch command: os.exec(vcs.exe(), [...]).

**Signature:** `extra.vcs.exe() → string`

**Returns:** string

