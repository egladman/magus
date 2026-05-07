# `fs`

Filesystem and path primitives.

> **Naming convention:** Teal/Lua binds each module per-import in `snake_case` (`local fs = require("magus.extra.fs")`, then `fs.some_method`). Buzz reaches them off the `import "magus/extra"` aggregate in `camelCase` (`extra.fs.someMethod`).

## Methods

### `glob`

Return paths matching pattern (doublestar-style).

**Signature (Teal):** `fs.glob(pattern) → []string`

**Signature (Buzz):** `extra.fs.glob(pattern) → []string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `pattern` | `string` |  | |

**Returns:** []string

### `dirname`

Directory portion of path.

**Signature (Teal):** `fs.dirname(path) → string`

**Signature (Buzz):** `extra.fs.dirname(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `basename`

Final element of path.

**Signature (Teal):** `fs.basename(path) → string`

**Signature (Buzz):** `extra.fs.basename(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `exists`

True iff path exists.

**Signature (Teal):** `fs.exists(path) → bool`

**Signature (Buzz):** `extra.fs.exists(path) → bool`

**Also in Buzz's stdlib:** `fs.exists` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

### `read_file`

Return the contents of path as a string.

**Signature (Teal):** `fs.read_file(path) → string`

**Signature (Buzz):** `extra.fs.readFile(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `write_file`

Write content to path (mode 0644).

**Signature (Teal):** `fs.write_file(path, content)`

**Signature (Buzz):** `extra.fs.writeFile(path, content)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `content` | `string` |  | |

### `mkdirall`

Create path and parents (default mode 0755).

**Signature (Teal):** `fs.mkdirall(path, [perm])`

**Signature (Buzz):** `extra.fs.mkdirall(path, [perm])`

**Also in Buzz's stdlib:** `fs.makeDirectory` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `perm` | `int` | yes | |

### `join`

Join path elements with the OS separator.

**Signature (Teal):** `fs.join(parts...) → string`

**Signature (Buzz):** `extra.fs.join(parts...) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `parts` | `string` |  | |

**Returns:** string

### `remove_all`

Recursively remove path (no error if missing).

**Signature (Teal):** `fs.remove_all(path)`

**Signature (Buzz):** `extra.fs.removeAll(path)`

**Also in Buzz's stdlib:** `fs.delete` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

### `list_dir`

Return directory entries; empty if path does not exist.

**Signature (Teal):** `fs.list_dir(path) → []string`

**Signature (Buzz):** `extra.fs.listDir(path) → []string`

**Also in Buzz's stdlib:** `fs.list` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** []string

### `watch`

Blocking. Watch paths (directories, recursively) and call callback with each debounced batch of changed paths until the callback returns true or the run is interrupted.

**Signature (Teal):** `fs.watch(paths, callback)`

**Signature (Buzz):** `extra.fs.watch(paths, callback)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `paths` | `[]string` |  | |
| `callback` | `Callback` |  | |

