---
title: archive module
generated_from: reference/buzz/
aliases: [modules/archive]
description: Archive creation and extraction with automatic format detection.
tags: [archive, module, stdlib, magusfile]
---

# archive

Archive creation and extraction with automatic format detection. Supports tar, zip, tar.gz, tar.bz2, tar.xz, and tar.zst. Symlinks and non-regular entries are skipped.

> **Naming convention:** import the module under its bare name (`import "archive"`), reach members with a backslash, and call methods in `camelCase`: `archive\someMethod`.

## Methods

### uncompress

Extract the archive at src into dest. Returns a table with fields: files (extracted paths relative to dest) and bytes (total uncompressed bytes written). opts is an Options: strip drops N leading path components, maxSize caps the uncompressed bytes (default 10 GiB), threads sets the parallel decode workers (0 = auto).

**Signature:** `archive\uncompress(src, dest, [opts]) -> UncompressResult` - [source](https://github.com/egladman/magus/blob/main/std/archive.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `src`     | `string`         |          |             |
| `dest`    | `string`         |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### compress

Create an archive at dest from src (a file or directory). Format is inferred from dest extension (.tar, .tar.gz, .tgz, .tar.zst, .zip). Returns a table with fields: files (archived paths relative to src), bytes_in (raw bytes read), bytes_out (compressed bytes written). opts is an Options: format overrides format detection, threads sets the parallel encode workers (0 = auto), level is the compression level (null = format default), followSymlinks archives what a link points at, maxSize caps the output bytes (default 10 GiB).

**Signature:** `archive\compress(src, dest, [opts]) -> CompressResult` - [source](https://github.com/egladman/magus/blob/main/std/archive.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `src`     | `string`         |          |             |
| `dest`    | `string`         |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** map[string]any

### list

List the archive at src without extracting it, as entries of {name, size, is_dir} sorted by name. Use it to check what an archive holds - that a release tarball carries the binary you expect, or how large an entry is - before paying to extract it. Symlinks and devices are skipped, matching uncompress. opts is an Options: threads sets the parallel decode workers (0 = auto).

**Signature:** `archive\list(src, [opts]) -> [ArchiveEntry]` - [source](https://github.com/egladman/magus/blob/main/std/archive.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `src`     | `string`         |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** any

### readFile

Return the contents of the single entry named name inside the archive at src, without extracting anything to disk. Reading one manifest out of a release tarball is the usual reason. Raises when name is not in the archive, or when the entry exceeds maxSize. opts is an Options: maxSize caps the entry (default 10 GiB), threads sets the parallel decode workers (0 = auto).

**Signature:** `archive\readFile(src, name, [opts]) -> string` - [source](https://github.com/egladman/magus/blob/main/std/archive.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `src`     | `string`         |          |             |
| `name`    | `string`         |          |             |
| `opts`    | `map[string]any` | yes      |             |

**Returns:** string

