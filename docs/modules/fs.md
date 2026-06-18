# `fs`

Filesystem and path primitives.

> **Naming convention:** import the module under its bare name (`import "fs"`) and call methods in `camelCase` (`fs.someMethod`).

## Methods

### `glob`

Return paths matching pattern (doublestar-style).

**Signature:** `fs.glob(pattern) → []string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `pattern` | `string` |  | |

**Returns:** []string

### `dirname`

Directory portion of path.

**Signature:** `fs.dirname(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `basename`

Final element of path.

**Signature:** `fs.basename(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `exists`

True iff path exists.

**Signature:** `fs.exists(path) → bool`

**Also in Buzz's stdlib:** `fs.exists` — the magus form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

### `read_file`

Return the contents of path as a string.

**Signature:** `fs.readFile(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `write_file`

Write content to path (mode 0644).

**Signature:** `fs.writeFile(path, content)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `content` | `string` |  | |

### `mkdirall`

Create path and parents (default mode 0755).

**Signature:** `fs.mkdirall(path, [perm])`

**Also in Buzz's stdlib:** `fs.makeDirectory` — the magus form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `perm` | `int` | yes | |

### `join`

Join path elements with the OS separator.

**Signature:** `fs.join(parts...) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `parts` | `string` |  | |

**Returns:** string

### `remove_all`

Recursively remove path (no error if missing).

**Signature:** `fs.removeAll(path)`

**Also in Buzz's stdlib:** `fs.delete` — the magus form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

### `list_dir`

Return directory entries; empty if path does not exist.

**Signature:** `fs.listDir(path) → []string`

**Also in Buzz's stdlib:** `fs.list` — the magus form is sandbox-aware.

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** []string

### `ext`

File-name extension of path, including the leading dot ("" if none).

**Signature:** `fs.ext(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `is_dir`

True iff path exists and is a directory (a sandbox-denied path reads as false).

**Signature:** `fs.isDir(path) → bool`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

### `is_file`

True iff path exists and is a regular file (a sandbox-denied path reads as false).

**Signature:** `fs.isFile(path) → bool`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

### `stat`

Return metadata for path as {size, mtime, mode, is_dir}: size in bytes, mtime as Unix millis, mode as the integer permission bits. Errors if path is missing.

**Signature:** `fs.stat(path) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** map[string]any

### `copy_file`

Copy the file at src to dst (overwriting), preserving its permission bits.

**Signature:** `fs.copyFile(src, dst)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `src` | `string` |  | |
| `dst` | `string` |  | |

### `copy_dir`

Recursively copy the directory tree at src to dst, preserving permission bits.

**Signature:** `fs.copyDir(src, dst)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `src` | `string` |  | |
| `dst` | `string` |  | |

### `watch`

Blocking. Watch paths (directories, recursively) and call callback with each debounced batch of changed paths until the callback returns true or the run is interrupted.

**Signature:** `fs.watch(paths, callback)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `paths` | `[]string` |  | |
| `callback` | `Callback` |  | |

### `walk`

Recursively walk the directory tree rooted at root, calling callback(path, is_dir) for each entry. Return true from callback to stop the walk early. Sandbox-denied entries are silently skipped.

**Signature:** `fs.walk(root, callback)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `root` | `string` |  | |
| `callback` | `Callback` |  | |

### `append_file`

Append content to path (creating if absent, mode 0644).

**Signature:** `fs.appendFile(path, content)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `content` | `string` |  | |

### `chmod`

Change the permission bits of path to mode (octal integer, e.g. 0755).

**Signature:** `fs.chmod(path, mode)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `mode` | `int` |  | |

### `symlink`

Create a symbolic link at link pointing to target.

**Signature:** `fs.symlink(target, link)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `target` | `string` |  | |
| `link` | `string` |  | |

### `readlink`

Return the target of the symbolic link at path.

**Signature:** `fs.readlink(path) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### `temp_dir`

Create a new temporary directory (in os.TempDir()) with an optional name prefix and return its path.

**Signature:** `fs.tempDir([prefix]) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `prefix` | `string` | yes | |

**Returns:** string

