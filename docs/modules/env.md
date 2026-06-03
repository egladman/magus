# `env`

Process environment variable access.

> **Naming convention:** Buzz reaches modules off the `import "magus/extra"` aggregate in `camelCase` (`extra.env.someMethod`).

## Methods

### `get`

Return the value of name, or "" if unset. Use lookup to tell unset from set-but-empty.

**Signature:** `extra.env.get(name) → string`

**Also in Buzz's stdlib:** `os.env` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

**Returns:** string

### `lookup`

Return (value, found); found is false when name is unset or stripped by the sandbox.

**Signature:** `extra.env.lookup(name) → string, bool`

**Also in Buzz's stdlib:** `os.env (returns null when unset)` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

**Returns:** string, bool

### `set`

Set name to value in the current process environment.

**Signature:** `extra.env.set(name, value)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |
| `value` | `string` |  | |

### `list`

Return all environment variables as a name→value map.

**Signature:** `extra.env.list() → map[string]string`

**Returns:** map[string]string

### `unset`

Remove name from the current process environment.

**Signature:** `extra.env.unset(name)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

### `expand`

Replace $VAR and ${VAR} references in s with their values (sandbox-stripped names expand to "").

**Signature:** `extra.env.expand(s) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### `home`

Return the current user's home directory.

**Signature:** `extra.env.home() → string`

**Returns:** string

