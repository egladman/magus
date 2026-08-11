---
title: fs module
aliases: [modules/fs]
description: Filesystem and path primitives.
tags: [fs, module, stdlib, magusfile]
---

# fs

Filesystem and path primitives.

> **Naming convention:** import the module under its bare name (`import "fs"`), reach members with a backslash, and call methods in `camelCase`: `fs\someMethod`.

<!-- -->

> [!NOTE]
> The examples below are reference-only. `fs` performs real IO (filesystem, process, network, or environment access) that the in-browser playground's sandbox cannot provide, so it is not registered there and its examples have no Run button. Pure-compute modules such as `strings` and `json` run their examples live in the page.

## Methods

### glob

Return paths matching pattern (doublestar-style).

**Signature:** `fs\glob(pattern) → [Path]` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L287)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `pattern` | `string` |  | |

**Returns:** any

**Example:**

```buzz
import "std";
import "fs";

foreach (path in fs\glob("cmd/**/*.go")) { std\print(path.value); }
```

### dirname

Directory portion of path.

**Signature:** `fs\dirname(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L329)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

**Example:**

```buzz
import "std";
import "fs";

std\print(fs\dirname("cmd/magus/main.go"));
// -> "cmd/magus"
```

### basename

Final element of path.

**Signature:** `fs\basename(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L334)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

**Example:**

```buzz
import "std";
import "fs";

std\print(fs\basename("cmd/magus/main.go"));
// -> "main.go"
```

### exists

True iff path exists.

**Signature:** `fs\exists(path) → bool`[^buzz-stdlib-fs-exists] · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L340)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

**Example:**

```buzz
import "std";
import "fs";

if (fs\exists("go.mod")) { std\print("Go module"); }
```

### readFile

Return the contents of path as a string.

**Signature:** `fs\readFile(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L360)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

**Example:**

```buzz
import "std";
import "fs";

final version = fs\readFile("VERSION");
std\print(version);
```

### writeFile

Write content to path (mode 0644).

**Signature:** `fs\writeFile(path, content)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L373)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `content` | `string` |  | |

**Example:**

```buzz
import "fs";

fs\writeFile("dist/manifest.txt", "artifact list here\n");
```

### mkdirAll

Create path and parents (default mode 0755).

**Signature:** `fs\mkdirAll(path, [perm])`[^buzz-stdlib-fs-mkdir_all] · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L388)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `perm` | `int` | yes | |

### join

Join path elements with the OS separator.

**Signature:** `fs\join(parts...) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L403)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `parts` | `string` |  | |

**Returns:** string

**Example:**

```buzz
import "std";
import "fs";

std\print(fs\join(["cmd", "magus", "main.go"]));
// -> "cmd/magus/main.go"
```

### removeAll

Recursively remove path (no error if missing).

**Signature:** `fs\removeAll(path)`[^buzz-stdlib-fs-remove_all] · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L408)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Example:**

```buzz
import "fs";

fs\removeAll("dist/");
```

### remove

Remove a single file or empty directory (no error if missing). Unlike remove_all it refuses a non-empty directory, so a wrong path costs one error rather than a recursive delete.

**Signature:** `fs\remove(path)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L424)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

### rename

Move or rename src to dst, creating dst's parent directory if needed. Within one filesystem this is atomic, which is what makes it the last step of a write-to-temp-then-swap. Across filesystems the underlying rename fails rather than silently copying; copy_file plus remove is the explicit form for that.

**Signature:** `fs\rename(src, dst)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L441)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `src` | `string` |  | |
| `dst` | `string` |  | |

### size

Return path's size in bytes. Raises when path does not exist; stat returns the whole FileInfo when more than the size is wanted.

**Signature:** `fs\size(path) → int` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L466)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** int

### tempFile

Create a new empty temporary file (in os.TempDir()) with an optional name prefix and return its path. The file is left in place for the caller to write and remove; temp_dir is the form for a whole tree.

**Signature:** `fs\tempFile([prefix]) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L479)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `prefix` | `string` | yes | |

**Returns:** string

### writeFileAtomic

Write content to path so a reader sees either the old bytes or the new ones, never a partial file: the content goes to a temporary file in the same directory, is flushed to disk, then renamed over path. Use it for anything another process may read while a target runs - a generated file, a lockfile, a cache index. write_file is the cheaper form when nothing else is looking.

**Signature:** `fs\writeFileAtomic(path, content)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L498)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `content` | `string` |  | |

### listDir

Return directory entries; empty if path does not exist.

**Signature:** `fs\listDir(path) → []string`[^buzz-stdlib-fs-list_dir] · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L551)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** []string

**Example:**

```buzz
import "std";
import "fs";

foreach (name in fs\listDir("cmd")) { std\print(name); }
```

### ext

File-name extension of path, including the leading dot ("" if none).

**Signature:** `fs\ext(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L571)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

**Example:**

```buzz
import "std";
import "fs";

std\print(fs\ext("archive.tar.gz"));
// -> ".gz"
```

### isDir

True iff path exists and is a directory. A sandbox-denied path raises rather than reading as false.

**Signature:** `fs\isDir(path) → bool` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L577)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

**Example:**

```buzz
import "std";
import "fs";

if (fs\isDir("internal")) { std\print("internal is a directory"); }
```

### isFile

True iff path exists and is a regular file. A sandbox-denied path raises rather than reading as false.

**Signature:** `fs\isFile(path) → bool` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L588)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** bool

**Example:**

```buzz
import "std";
import "fs";

if (fs\isFile("go.mod")) { std\print("go.mod is a file"); }
```

### stat

Return metadata for path as {size, mtime, mode, is_dir}: size in bytes, mtime as Unix millis, mode as the integer permission bits. Errors if path is missing.

**Signature:** `fs\stat(path) → FileInfo` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L600)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** map[string]any

**Example:**

```buzz
import "std";
import "fs";

// Bracket access, not info.size: `size` is a built-in map METHOD, so dot access returns
// the method rather than the stat field. The time key is `mtime` (Unix millis), not
// `modTime` - dot access on a missing key is silent, which is how this example went
// unnoticed while printing nothing useful.
final info = fs\stat("go.mod");
std\print(info["size"]);
std\print(info["mtime"]);
```

### copyFile

Copy the file at src to dst (overwriting), preserving its permission bits.

**Signature:** `fs\copyFile(src, dst)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L619)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `src` | `string` |  | |
| `dst` | `string` |  | |

**Example:**

```buzz
import "fs";

fs\copyFile("dist/magus", "/usr/local/bin/magus");
```

### copyDir

Recursively copy the directory tree at src to dst, preserving permission bits.

**Signature:** `fs\copyDir(src, dst)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L639)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `src` | `string` |  | |
| `dst` | `string` |  | |

**Example:**

```buzz
import "fs";

// Recursive copy; preserves file mode and dir structure.
fs\copyDir("assets/", "dist/assets/");
```

### watch

Blocking. Watch paths (directories, recursively) and call callback with each debounced batch of changed paths until the callback returns true or the run is interrupted.

**Signature:** `fs\watch(paths, callback)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L740)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `paths` | `[]string` |  | |
| `callback` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L23) |  | |

**Example:**

```buzz
import "std";
import "fs";

// Blocks; the callback fires per change batch. Return true to keep watching.
fs\watch(["cmd/**/*.go", "internal/**/*.go"], fun (paths: [str]) > bool {
    foreach (p in paths) { std\print("changed: " + p); }
    return true;
});
```

### walk

Recursively walk the directory tree rooted at root, calling callback(path, is_dir) for each entry. Return true from callback to stop the walk early. Sandbox-denied entries are silently skipped.

**Signature:** `fs\walk(root, callback)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L797)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `root` | `string` |  | |
| `callback` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L23) |  | |

**Example:**

```buzz
import "std";
import "fs";

fs\walk(".", fun (path: str, isDir: bool) > bool {
    if (isDir and fs\basename(path) == "node_modules") {
        return false;   // skip descent
    }
    if (fs\ext(path) == ".go") { std\print(path); }
    return true;
});
```

### appendFile

Append content to path (creating if absent, mode 0644).

**Signature:** `fs\appendFile(path, content)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L834)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `content` | `string` |  | |

**Example:**

```buzz
import "fs";

fs\appendFile("dist/build.log", "compile done\n");
```

### chmod

Change the permission bits of path to mode (octal integer, e.g. 0755).

**Signature:** `fs\chmod(path, mode)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L857)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `mode` | `int` |  | |

**Example:**

```buzz
import "fs";

// Mark the release binary executable. Buzz has no octal literal
// (matches upstream); Unix mode 0755 = 493 decimal.
fs\chmod("dist/magus", 493);
```

### symlink

Create a symbolic link at link pointing to target.

**Signature:** `fs\symlink(target, link)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L873)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `target` | `string` |  | |
| `link` | `string` |  | |

**Example:**

```buzz
import "fs";

fs\symlink("dist/magus", "/usr/local/bin/magus");
```

### readlink

Return the target of the symbolic link at path.

**Signature:** `fs\readlink(path) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L891)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** string

**Example:**

```buzz
import "std";
import "fs";

std\print(fs\readlink("/usr/local/bin/magus"));
```

### tempDir

Create a new temporary directory (in os.TempDir()) with an optional name prefix and return its path.

**Signature:** `fs\tempDir([prefix]) → string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L934)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `prefix` | `string` | yes | |

**Returns:** string

**Example:**

```buzz
import "std";
import "fs";

final tmp = fs\tempDir("magus-build-");
std\print(tmp);
// -> "/tmp/magus-build-abc123"
```

### readLines

Read path and return its lines as a list, with the line terminators stripped. A single trailing newline yields no extra empty element; an empty file yields an empty list.

**Signature:** `fs\readLines(path) → []string` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L907)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |

**Returns:** []string

**Example:**

```buzz
import "std";
import "fs";

foreach (line in fs\readLines("targets.txt")) { std\print(line); }
```

### writeLines

Write lines to path (mode 0644), each followed by a newline. The companion to read_lines: write_lines(p, read_lines(p)) round-trips a newline-terminated file.

**Signature:** `fs\writeLines(path, lines)` · [source](https://github.com/egladman/magus/blob/main/std/fs.go#L921)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `path` | `string` |  | |
| `lines` | `[]string` |  | |

**Example:**

```buzz
import "fs";

fs\writeLines("dist/targets.txt", ["build", "test", "lint"]);
```

[^buzz-stdlib-fs-exists]: `fs\exists` is also in Buzz's standard library (`fs.exists`); the magus form is sandbox-aware.
[^buzz-stdlib-fs-mkdir_all]: `fs\mkdirAll` is also in Buzz's standard library (`fs.makeDirectory`); the magus form is sandbox-aware.
[^buzz-stdlib-fs-remove_all]: `fs\removeAll` is also in Buzz's standard library (`fs.delete`); the magus form is sandbox-aware.
[^buzz-stdlib-fs-list_dir]: `fs\listDir` is also in Buzz's standard library (`fs.list`); the magus form is sandbox-aware.
