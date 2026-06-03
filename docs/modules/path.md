# `path`

Pure path-string math: abs, rel, clean, is_abs, expand_user.

> **Naming convention:** Buzz reaches modules off the `import "magus/extra"` aggregate in `camelCase` (`extra.path.someMethod`).

## Methods

### `abs`

Return the absolute form of path, resolved against the current directory and lexically cleaned.

**Signature:** `extra.path.abs(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `rel`

Return a relative path from base to target; errors if no relative path exists.

**Signature:** `extra.path.rel(base, target) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `base` | `string` |  | |
| `target` | `string` |  | |

**Returns:** string

### `clean`

Return the shortest lexically-equivalent path (resolves . and .., collapses separators).

**Signature:** `extra.path.clean(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `is_abs`

Report whether path is absolute.

**Signature:** `extra.path.isAbs(path) → bool`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

### `expand_user`

Expand a leading ~ (or ~/...) to the current user's home directory; other paths are returned unchanged.

**Signature:** `extra.path.expandUser(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

