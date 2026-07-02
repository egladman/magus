---
title: fs module
description: Filesystem primitives for magusfiles - glob, read, write, stat, walk, watch, copy, mkdirall, symlinks, and temp dirs. Sandbox-aware over Buzz.
tags: [fs, filesystem, glob, read file, write file, walk, watch, stat, magus stdlib]
---

# fs

Filesystem and path primitives.

> **Naming convention:** import the module under its bare name (`import "fs"`) and call methods in `camelCase` (`fs.someMethod`).

## Methods

### glob

Return paths matching pattern (doublestar-style).

**Signature:** `fs.glob(pattern) → []string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L219)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `pattern` | `string` |  | |

**Returns:** []string

### dirname

Directory portion of path.

**Signature:** `fs.dirname(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L246)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### basename

Final element of path.

**Signature:** `fs.basename(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L251)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### exists

True iff path exists.

**Signature:** `fs.exists(path) → bool`[^buzz-stdlib-fs-exists] · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L256)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

### read_file

Return the contents of path as a string.

**Signature:** `fs.readFile(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L270)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### write_file

Write content to path (mode 0644).

**Signature:** `fs.writeFile(path, content)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L283)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `content` | `string` |  | |

### mkdirall

Create path and parents (default mode 0755).

**Signature:** `fs.mkdirall(path, [perm])`[^buzz-stdlib-fs-mkdirall] · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L298)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `perm` | `int` | yes | |

### join

Join path elements with the OS separator.

**Signature:** `fs.join(parts...) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L313)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `parts` | `string` |  | |

**Returns:** string

### remove_all

Recursively remove path (no error if missing).

**Signature:** `fs.removeAll(path)`[^buzz-stdlib-fs-remove_all] · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L318)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

### list_dir

Return directory entries; empty if path does not exist.

**Signature:** `fs.listDir(path) → []string`[^buzz-stdlib-fs-list_dir] · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L333)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** []string

### ext

File-name extension of path, including the leading dot ("" if none).

**Signature:** `fs.ext(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L353)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### is_dir

True iff path exists and is a directory (a sandbox-denied path reads as false).

**Signature:** `fs.isDir(path) → bool` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L360)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

### is_file

True iff path exists and is a regular file (a sandbox-denied path reads as false).

**Signature:** `fs.isFile(path) → bool` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L371)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

### stat

Return metadata for path as {size, mtime, mode, is_dir}: size in bytes, mtime as Unix millis, mode as the integer permission bits. Errors if path is missing.

**Signature:** `fs.stat(path) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L383)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** map[string]any

### copy_file

Copy the file at src to dst (overwriting), preserving its permission bits.

**Signature:** `fs.copyFile(src, dst)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L402)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `src` | `string` |  | |
| `dst` | `string` |  | |

### copy_dir

Recursively copy the directory tree at src to dst, preserving permission bits.

**Signature:** `fs.copyDir(src, dst)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L422)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `src` | `string` |  | |
| `dst` | `string` |  | |

### watch

Blocking. Watch paths (directories, recursively) and call callback with each debounced batch of changed paths until the callback returns true or the run is interrupted.

**Signature:** `fs.watch(paths, callback)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L520)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `paths` | `[]string` |  | |
| `callback` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L18) |  | |

### walk

Recursively walk the directory tree rooted at root, calling callback(path, is_dir) for each entry. Return true from callback to stop the walk early. Sandbox-denied entries are silently skipped.

**Signature:** `fs.walk(root, callback)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L570)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `root` | `string` |  | |
| `callback` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L18) |  | |

### append_file

Append content to path (creating if absent, mode 0644).

**Signature:** `fs.appendFile(path, content)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L607)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `content` | `string` |  | |

### chmod

Change the permission bits of path to mode (octal integer, e.g. 0755).

**Signature:** `fs.chmod(path, mode)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L630)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `mode` | `int` |  | |

### symlink

Create a symbolic link at link pointing to target.

**Signature:** `fs.symlink(target, link)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L646)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `target` | `string` |  | |
| `link` | `string` |  | |

### readlink

Return the target of the symbolic link at path.

**Signature:** `fs.readlink(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L664)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

### temp_dir

Create a new temporary directory (in os.TempDir()) with an optional name prefix and return its path.

**Signature:** `fs.tempDir([prefix]) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L707)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `prefix` | `string` | yes | |

**Returns:** string

### read_lines

Read path and return its lines as a list, with the line terminators stripped. A single trailing newline yields no extra empty element; an empty file yields an empty list.

**Signature:** `fs.readLines(path) → []string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L680)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** []string

### write_lines

Write lines to path (mode 0644), each followed by a newline. The companion to read_lines: write_lines(p, read_lines(p)) round-trips a newline-terminated file.

**Signature:** `fs.writeLines(path, lines)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L694)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `lines` | `[]string` |  | |

[^buzz-stdlib-fs-exists]: `fs.exists` is also in Buzz's standard library (`fs.exists`); the magus form is sandbox-aware.
[^buzz-stdlib-fs-mkdirall]: `fs.mkdirall` is also in Buzz's standard library (`fs.makeDirectory`); the magus form is sandbox-aware.
[^buzz-stdlib-fs-remove_all]: `fs.removeAll` is also in Buzz's standard library (`fs.delete`); the magus form is sandbox-aware.
[^buzz-stdlib-fs-list_dir]: `fs.listDir` is also in Buzz's standard library (`fs.list`); the magus form is sandbox-aware.
