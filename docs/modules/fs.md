# `fs`

Filesystem and path primitives.

> **Naming convention:** Buzz reaches modules off the `import "magus/extra"` aggregate in `camelCase` (`extra.fs.someMethod`).

## Methods

### `glob`

Return paths matching pattern (doublestar-style).

**Signature:** `extra.fs.glob(pattern) → []string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `pattern` | `string` |  | |

**Returns:** []string

### `dirname`

Directory portion of path.

**Signature:** `extra.fs.dirname(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `basename`

Final element of path.

**Signature:** `extra.fs.basename(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `exists`

True iff path exists.

**Signature:** `extra.fs.exists(path) → bool`

**Also in Buzz's stdlib:** `fs.exists` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

### `read_file`

Return the contents of path as a string.

**Signature:** `extra.fs.readFile(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `write_file`

Write content to path (mode 0644).

**Signature:** `extra.fs.writeFile(path, content)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `content` | `string` |  | |

### `mkdirall`

Create path and parents (default mode 0755).

**Signature:** `extra.fs.mkdirall(path, [perm])`

**Also in Buzz's stdlib:** `fs.makeDirectory` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `perm` | `int` | yes | |

### `join`

Join path elements with the OS separator.

**Signature:** `extra.fs.join(parts...) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `parts` | `string` |  | |

**Returns:** string

### `remove_all`

Recursively remove path (no error if missing).

**Signature:** `extra.fs.removeAll(path)`

**Also in Buzz's stdlib:** `fs.delete` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

### `list_dir`

Return directory entries; empty if path does not exist.

**Signature:** `extra.fs.listDir(path) → []string`

**Also in Buzz's stdlib:** `fs.list` — the `extra` form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** []string

### `ext`

File-name extension of path, including the leading dot ("" if none).

**Signature:** `extra.fs.ext(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `is_dir`

True iff path exists and is a directory (a sandbox-denied path reads as false).

**Signature:** `extra.fs.isDir(path) → bool`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

### `is_file`

True iff path exists and is a regular file (a sandbox-denied path reads as false).

**Signature:** `extra.fs.isFile(path) → bool`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

### `stat`

Return metadata for path as {size, mtime, mode, is_dir}: size in bytes, mtime as Unix millis, mode as the integer permission bits. Errors if path is missing.

**Signature:** `extra.fs.stat(path) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** map[string]any

### `copy_file`

Copy the file at src to dst (overwriting), preserving its permission bits.

**Signature:** `extra.fs.copyFile(src, dst)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `src` | `string` |  | |
| `dst` | `string` |  | |

### `copy_dir`

Recursively copy the directory tree at src to dst, preserving permission bits.

**Signature:** `extra.fs.copyDir(src, dst)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `src` | `string` |  | |
| `dst` | `string` |  | |

### `watch`

Blocking. Watch paths (directories, recursively) and call callback with each debounced batch of changed paths until the callback returns true or the run is interrupted.

**Signature:** `extra.fs.watch(paths, callback)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `paths` | `[]string` |  | |
| `callback` | `Callback` |  | |

