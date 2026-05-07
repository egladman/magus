# `env`

Process environment variable access.

> **Naming convention:** Teal/Lua binds each module per-import in `snake_case` (`local env = require("magus.extra.env")`, then `env.some_method`). Buzz reaches them off the `import "magus/extra"` aggregate in `camelCase` (`extra.env.someMethod`).

## Methods

### `get`

Return the value of name, or "" if unset. Use lookup to tell unset from set-but-empty.

**Signature (Teal):** `env.get(name) → string`

**Signature (Buzz):** `extra.env.get(name) → string`

**Also in Buzz's stdlib:** `os.env` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

**Returns:** string

### `lookup`

Return (value, found); found is false when name is unset or stripped by the sandbox.

**Signature (Teal):** `env.lookup(name) → string, bool`

**Signature (Buzz):** `extra.env.lookup(name) → string, bool`

**Also in Buzz's stdlib:** `os.env (returns null when unset)` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

**Returns:** string, bool

### `set`

Set name to value in the current process environment.

**Signature (Teal):** `env.set(name, value)`

**Signature (Buzz):** `extra.env.set(name, value)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |
| `value` | `string` |  | |

### `list`

Return all environment variables as a name→value map.

**Signature (Teal):** `env.list() → map[string]string`

**Signature (Buzz):** `extra.env.list() → map[string]string`

**Returns:** map[string]string

