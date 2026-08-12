---
title: archive module
aliases: [modules/archive]
description: Archive creation and extraction with automatic format detection.
tags: [archive, module, stdlib, magusfile]
---

# archive

Archive creation and extraction with automatic format detection. Supports tar, zip, tar.gz, tar.bz2, tar.xz, and tar.zst. Symlinks and non-regular entries are skipped.

> **Naming convention:** import the module under its bare name (`import "archive"`), reach members with a backslash, and call methods in `camelCase`: `archive\someMethod`.

## Methods

### uncompress

Extract the archive at src into dest. Returns a table with fields: files (extracted paths relative to dest) and bytes (total uncompressed bytes written). opts keys: strip (int, strip N leading path components), max_size (int, uncompressed byte cap, default 10 GiB), threads (int, parallel decode workers; 0 or omitted = auto).

**Signature:** `archive\uncompress(src, dest, [opts]) → UncompressResult` · [source](https://github.com/egladman/magus/blob/main/std/archive.go#L405)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `src` | `string` |  | |
| `dest` | `string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### compress

Create an archive at dest from src (a file or directory). Format is inferred from dest extension (.tar, .tar.gz, .tgz, .tar.zst, .zip). Returns a table with fields: files (archived paths relative to src), bytes_in (raw bytes read), bytes_out (compressed bytes written). opts keys: format (string, override format detection), threads (int, parallel encode workers; 0 or omitted = auto), level (int, compression level; -1 = format default), follow_symlinks (bool, default false), max_size (int, output byte cap, default 10 GiB).

**Signature:** `archive\compress(src, dest, [opts]) → CompressResult` · [source](https://github.com/egladman/magus/blob/main/std/archive.go#L474)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `src` | `string` |  | |
| `dest` | `string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### list

List the archive at src without extracting it, as entries of {name, size, is_dir} sorted by name. Use it to check what an archive holds - that a release tarball carries the binary you expect, or how large an entry is - before paying to extract it. Symlinks and devices are skipped, matching uncompress. opts keys: threads (int, parallel decode workers; 0 or omitted = auto).

**Signature:** `archive\list(src, [opts]) → [ArchiveEntry]` · [source](https://github.com/egladman/magus/blob/main/std/archive.go#L193)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `src` | `string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** any

### readFile

Return the contents of the single entry named name inside the archive at src, without extracting anything to disk. Reading one manifest out of a release tarball is the usual reason. Raises when name is not in the archive, or when the entry exceeds max_size. opts keys: max_size (int, byte cap on the entry, default 10 GiB), threads (int, parallel decode workers; 0 or omitted = auto).

**Signature:** `archive\readFile(src, name, [opts]) → string` · [source](https://github.com/egladman/magus/blob/main/std/archive.go#L274)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `src` | `string` |  | |
| `name` | `string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** string

