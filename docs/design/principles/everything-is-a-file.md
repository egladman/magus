---
title: Everything is a file
order: 5
description: Local state is JSON Lines a person reads with cat, grep and diff; one entry per line, the last line per key wins, every rewrite a rename.
tags: [design, principle, state, unix]
---

# Everything is a file

magus keeps its local state in files a person can read with the tools already
on the machine. A store is one file of JSON Lines: one entry per line, one
JSON object per entry. `cat` reads a store, `grep` finds an entry, `diff`
compares two, `tail` follows one and `wc -l` counts it. None of that needs
magus running or a parser for its format.

The idea is old, and magus borrows from it selectively. Unix made the line the
record, so `grep`, `sort`, `cut` and `wc` work on any file that keeps to it.
git stores an object under a two-character fan-out of its hash and a ref as
one line naming a commit, updated through a lock file and a rename. Maildir
keeps one message per file and delivers by renaming from `tmp/` into `new/`,
so no reader sees half a message and no writer takes a lock. `/proc` exposes
one value per file. Plan 9 went furthest, with every service a file tree.
magus takes what fits a store on disk: the layout, the line, the rename. It
does not take synthetic file servers; the daemon still answers over RPC.

The failure this prevents is state nobody can inspect when it matters. A
stuck run, a lease that will not release, or a line that reads wrong is
debugged at a terminal with whatever is installed, often after the process
that wrote it is gone. A format only magus can read puts the broken tool
between you and the evidence of what broke it, and one document holding every
entry turns one corrupt write into a lost store; a corrupt line costs the
line.

### The conventions

- **Layout and naming.** A store lives under the workspace's `.magus/` or a
  user directory resolved by [`libs/xdg`](https://github.com/egladman/magus/tree/main/libs/xdg) (`StateDir`, `CacheDir`), never a
  literal path. A directory names the kind and the
  file in it is the store (`jobs.jsonl`), so the listing is the index. A
  content-addressed store of blobs fans out on the first two hex digits of the
  hash, as git's does (`cas/ab/ab12...`).
- **JSON Lines.** A store is one append-only `.jsonl` file: one JSON object
  per entry, one entry per line, each line ending in a newline. A single JSON
  document, whose entries only a parser can find, is not a store format, and
  neither is a `name<TAB>value` pair or a file per entry holding a document.
- **The last line for a key wins.** A change appends a line for the entry's
  key and a reader keeps the last one it finds. Compaction rewrites the file
  to one line per key through a rename and never edits a line in place, once
  the file holds more than twice as many lines as keys, plus 64. A
  store a person edits by hand, which nothing compacts, refuses a repeated key
  instead (the conformance ratchet, [`hack/policy/conformance.jsonl`](https://github.com/egladman/magus/blob/main/hack/policy/conformance.jsonl)).
- **Atomic write via rename.** A rewrite goes to a temporary file in the same
  directory and is renamed over the old one (`file.WriteFileAtomic`, which
  also syncs). A reader sees the old file or the new one, never half of
  either. An append is one write of one complete line.
- **Encoding lives on the type.** An entry type owns its encoding in
  `MarshalJSON` and `UnmarshalJSON`. A reader hands each line to the type and
  never splits, scans or pattern-matches a line itself. A store a person
  edits decodes strictly ([`internal/json`](https://github.com/egladman/magus/tree/main/internal/json)'s `StrictDecoder`): an unknown
  field, a duplicate key, a wrong kind or a bad value is an error naming
  `path:line`. A store only magus writes may skip a field a newer magus added.
- **An old format is named, never read.** When a store changes shape, the old
  file is not read as a second format and not skipped in silence: the error
  names it and says how to move it aside.
- **Readable with cat, grep and diff.** Text is UTF-8. The one binary
  exception is a blob holding the bytes a tool produced, stored verbatim.
- **One owner.** Only the package that owns a store knows where it lives.
  Another package calls the owner's reader or writer and never builds, stats or
  opens the path itself.

### Debt and exceptions

The stores that do not follow the format yet, as found on 2026-10-14:

- **Runtime records** are a capped log kept as one rewritten JSON array in the
  knowledge graph store. The state cannot be rebuilt, so this is debt owed a
  conversion to appended lines.
- **Owed regenerations** are one rewritten JSON document in [`vcs/owed.go`](https://github.com/egladman/magus/blob/main/vcs/owed.go). The
  state cannot be rebuilt, so this is debt owed a conversion to appended lines.
- **Name and value pairs.** The lock, pipe and watchdog records
  ([`internal/file/record`](https://github.com/egladman/magus/tree/main/internal/file/record)) write one `name<TAB>value` pair per line and want
  one JSON object per line.
- **Derived, rebuildable caches** are accelerators, exempt from the rule. The
  rule covers state that cannot be rebuilt, and a cache that is deleted and
  recomputed loses nothing.
- **One file per entry, a JSON document inside.** Cache manifests, run
  descriptors, knowledge-graph shards, merge-queue verdicts, the service
  journal and connector records break the one-file and the line rules, and stay
  an exception.

## Enforced by

- `file.JSONL` gives a keyed store its append, its last-line read, its
  compaction and its lock.
- `file.WriteFileAtomic` gives every rewrite its rename.
- [`internal/json`](https://github.com/egladman/magus/tree/main/internal/json) gives every line its strict decoder.
- The `store-path-export` conformance metric fails an exported path of a
  state-writing package that code outside it references; the shared root
  resolvers (`config.CacheDir`, `config.StoreDir`, `vcs.StateDir`) are listed in
  the ratchet with the reason.
- Nothing refuses a new JSON document, so the format itself holds in review
  alone.

## Good

One entry per line; a change appends, and the last line for a key wins.

```jsonl
{"id":"docs-index","state":"open","model":"economy"}
{"id":"docs-index","state":"pass","model":"economy"}
```

## Bad

```json
{
  "jobs": [
    { "id": "docs-index", "state": "pass", "model": "economy" }
  ]
}
```

Every change rewrites the whole document, `grep` cannot find an entry without
a parser, and one torn write loses every job in it.

## See also

- [Behavior is code, facts are data](behavior-is-code-facts-are-data.md)
- [A cache is an accelerator, never a dependency](a-cache-is-an-accelerator.md)
