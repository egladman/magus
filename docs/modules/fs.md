---
title: fs module
description: Filesystem and path primitives.
tags: [fs, module, stdlib, magusfile]
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

**Example:**

```buzz
import "std";
import "fs";

foreach (path in fs.glob("cmd/**/*.go")) { std.print(path); }
```

### dirname

Directory portion of path.

**Signature:** `fs.dirname(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L246)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

**Example:**

```buzz
import "std";
import "fs";

std.print(fs.dirname("cmd/magus/main.go"));
// -> "cmd/magus"
```

### basename

Final element of path.

**Signature:** `fs.basename(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L251)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

**Example:**

```buzz
import "std";
import "fs";

std.print(fs.basename("cmd/magus/main.go"));
// -> "main.go"
```

### exists

True iff path exists.

**Signature:** `fs.exists(path) → bool`[^buzz-stdlib-fs-exists] · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L256)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

**Example:**

```buzz
import "std";
import "fs";

if (fs.exists("go.mod")) { std.print("Go module"); }
```

### read_file

Return the contents of path as a string.

**Signature:** `fs.readFile(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L270)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

**Example:**

```buzz
import "std";
import "fs";

final version = fs.readFile("VERSION");
std.print(version);
```

### write_file

Write content to path (mode 0644).

**Signature:** `fs.writeFile(path, content)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L283)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `content` | `string` |  | |

**Example:**

```buzz
import "fs";

fs.writeFile("dist/manifest.txt", "artifact list here\n");
```

### mkdirall

Create path and parents (default mode 0755).

**Signature:** `fs.mkdirall(path, [perm])`[^buzz-stdlib-fs-mkdirall] · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L298)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `perm` | `int` | yes | |

**Example:**

```buzz
import "fs";

// Buzz has no octal literal (matches upstream); Unix mode 0755 = 493 decimal.
fs.mkdirall("dist/reports", 493);
```

### join

Join path elements with the OS separator.

**Signature:** `fs.join(parts...) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L313)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `parts` | `string` |  | |

**Returns:** string

**Example:**

```buzz
import "std";
import "fs";

std.print(fs.join(["cmd", "magus", "main.go"]));
// -> "cmd/magus/main.go"
```

### remove_all

Recursively remove path (no error if missing).

**Signature:** `fs.removeAll(path)`[^buzz-stdlib-fs-remove_all] · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L318)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Example:**

```buzz
import "fs";

fs.removeAll("dist/");
```

### list_dir

Return directory entries; empty if path does not exist.

**Signature:** `fs.listDir(path) → []string`[^buzz-stdlib-fs-list_dir] · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L333)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** []string

**Example:**

```buzz
import "std";
import "fs";

foreach (name in fs.listDir("cmd")) { std.print(name); }
```

### ext

File-name extension of path, including the leading dot ("" if none).

**Signature:** `fs.ext(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L353)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

**Example:**

```buzz
import "std";
import "fs";

std.print(fs.ext("archive.tar.gz"));
// -> ".gz"
```

### is_dir

True iff path exists and is a directory (a sandbox-denied path reads as false).

**Signature:** `fs.isDir(path) → bool` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L360)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

**Example:**

```buzz
import "std";
import "fs";

if (fs.isDir("internal")) { std.print("internal is a directory"); }
```

### is_file

True iff path exists and is a regular file (a sandbox-denied path reads as false).

**Signature:** `fs.isFile(path) → bool` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L371)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

**Example:**

```buzz
import "std";
import "fs";

if (fs.isFile("go.mod")) { std.print("go.mod is a file"); }
```

### stat

Return metadata for path as {size, mtime, mode, is_dir}: size in bytes, mtime as Unix millis, mode as the integer permission bits. Errors if path is missing.

**Signature:** `fs.stat(path) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L383)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** map[string]any

**Example:**

```buzz
import "std";
import "fs";

final info = fs.stat("go.mod");
std.print(info.size);
std.print(info.modTime);
```

### copy_file

Copy the file at src to dst (overwriting), preserving its permission bits.

**Signature:** `fs.copyFile(src, dst)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L402)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `src` | `string` |  | |
| `dst` | `string` |  | |

**Example:**

```buzz
import "fs";

fs.copyFile("dist/magus", "/usr/local/bin/magus");
```

### copy_dir

Recursively copy the directory tree at src to dst, preserving permission bits.

**Signature:** `fs.copyDir(src, dst)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L422)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `src` | `string` |  | |
| `dst` | `string` |  | |

**Example:**

```buzz
import "fs";

// Recursive copy; preserves file mode and dir structure.
fs.copyDir("assets/", "dist/assets/");
```

### watch

Blocking. Watch paths (directories, recursively) and call callback with each debounced batch of changed paths until the callback returns true or the run is interrupted.

**Signature:** `fs.watch(paths, callback)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L520)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `paths` | `[]string` |  | |
| `callback` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L18) |  | |

**Example:**

```buzz
import "std";
import "fs";

// Blocks; the callback fires per change batch. Return true to keep watching.
fs.watch(["cmd/**/*.go", "internal/**/*.go"], fun (paths: [str]) > bool {
    foreach (p in paths) { std.print("changed: " + p); }
    return true;
});
```

### walk

Recursively walk the directory tree rooted at root, calling callback(path, is_dir) for each entry. Return true from callback to stop the walk early. Sandbox-denied entries are silently skipped.

**Signature:** `fs.walk(root, callback)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L570)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `root` | `string` |  | |
| `callback` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L18) |  | |

**Example:**

```buzz
import "std";
import "fs";

fs.walk(".", fun (path: str, isDir: bool) > bool {
    if (isDir and fs.basename(path) == "node_modules") {
        return false;   // skip descent
    }
    if (fs.ext(path) == ".go") { std.print(path); }
    return true;
});
```

### append_file

Append content to path (creating if absent, mode 0644).

**Signature:** `fs.appendFile(path, content)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L607)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `content` | `string` |  | |

**Example:**

```buzz
import "fs";

fs.appendFile("dist/build.log", "compile done\n");
```

### chmod

Change the permission bits of path to mode (octal integer, e.g. 0755).

**Signature:** `fs.chmod(path, mode)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L630)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `mode` | `int` |  | |

**Example:**

```buzz
import "fs";

// Mark the release binary executable. Buzz has no octal literal
// (matches upstream); Unix mode 0755 = 493 decimal.
fs.chmod("dist/magus", 493);
```

### symlink

Create a symbolic link at link pointing to target.

**Signature:** `fs.symlink(target, link)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L646)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `target` | `string` |  | |
| `link` | `string` |  | |

**Example:**

```buzz
import "fs";

fs.symlink("dist/magus", "/usr/local/bin/magus");
```

### readlink

Return the target of the symbolic link at path.

**Signature:** `fs.readlink(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L664)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

**Example:**

```buzz
import "std";
import "fs";

std.print(fs.readlink("/usr/local/bin/magus"));
```

### temp_dir

Create a new temporary directory (in os.TempDir()) with an optional name prefix and return its path.

**Signature:** `fs.tempDir([prefix]) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L707)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `prefix` | `string` | yes | |

**Returns:** string

**Example:**

```buzz
import "std";
import "fs";

final tmp = fs.tempDir("magus-build-");
std.print(tmp);
// -> "/tmp/magus-build-abc123"
```

### read_lines

Read path and return its lines as a list, with the line terminators stripped. A single trailing newline yields no extra empty element; an empty file yields an empty list.

**Signature:** `fs.readLines(path) → []string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L680)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** []string

**Example:**

```buzz
import "std";
import "fs";

foreach (line in fs.readLines("targets.txt")) { std.print(line); }
```

### write_lines

Write lines to path (mode 0644), each followed by a newline. The companion to read_lines: write_lines(p, read_lines(p)) round-trips a newline-terminated file.

**Signature:** `fs.writeLines(path, lines)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L694)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `lines` | `[]string` |  | |

**Example:**

```buzz
import "fs";

fs.writeLines("dist/targets.txt", ["build", "test", "lint"]);
```

[^buzz-stdlib-fs-exists]: `fs.exists` is also in Buzz's standard library (`fs.exists`); the magus form is sandbox-aware.
[^buzz-stdlib-fs-mkdirall]: `fs.mkdirall` is also in Buzz's standard library (`fs.makeDirectory`); the magus form is sandbox-aware.
[^buzz-stdlib-fs-remove_all]: `fs.removeAll` is also in Buzz's standard library (`fs.delete`); the magus form is sandbox-aware.
[^buzz-stdlib-fs-list_dir]: `fs.listDir` is also in Buzz's standard library (`fs.list`); the magus form is sandbox-aware.
