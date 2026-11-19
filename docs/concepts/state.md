---
title: State
description: "Where magus keeps local state, the JSON Lines format every store shares, and how a store moves to a new shape: versioned, up-only migrations applied once, through a rename, and said out loud."
tags: [state, stores, json-lines, migrations, job-store, schema, version]
---

# State

magus keeps its local state in stores: one JSON Lines file per store, one JSON
object per line, the last line for a key being the entry. A change appends a
line; compaction rewrites the file to one line per key through a rename. The
[everything is a file](../design/principles/everything-is-a-file.md) principle
holds the full set of conventions.

<!--diagram:sessions-->

## Store migrations

A store whose entries change shape, such as a member renamed, migrates the way a
Kubernetes conversion takes an object between versions of its group. Each change
is a migration: an ordered step that converts every entry, one at a time, to the
next store version, an api version such as `safetyscissors.job/v1alpha2`. The
store knows its migrations in order, and the versions they convert to order the
same way.

The version lives in the store's own header line, as its `api_version`:

```jsonl
{"api_version":"safetyscissors.job/v1alpha2","kind":"JobList"}
{"api_version":"safetyscissors.job/v1alpha1","kind":"Job","metadata":{"name":"docs-index"},"spec":{"agent":{"instructions":"index the docs","worker":{"type":"resumed"}}}}
```

A header that names no `api_version`, or one in a group no migration converts
to, predates the store's first migration.

When magus opens a store:

- **At its version**, the store is read as it is.
- **Older**, magus takes the store's lock and applies every migration past the
  header's version, in order, to every line. It writes the result to a
  temporary file beside the store and renames it into place, so a reader sees
  the old file or the migrated one, never half of either. A migration that fails
  leaves the old file as it was. magus then says what it did:

  ```text
  job: migrated job store ~/.local/state/magus/jobs/.../jobs.jsonl from safetyscissors.job/v1alpha1 to safetyscissors.job/v1alpha2 (14 entries)
  ```

  Each store migrates once. The next magus to open it finds it current, and
  no reader keeps the old shape.

- **Older, from a binary off the base**, the job store is refused, and nothing
  is read or written. Only a magus built on a clean commit the workspace's base
  contains migrates it. Every checkout and branch shares the file, so a binary
  built from a branch would move it past every magus built on the base. Rebuild
  magus from the base, or run the branch binary under its own `XDG_STATE_HOME`.
  The header names the magus that wrote the store last, and each refusal names
  it.

- **Newer** than this magus knows, the store is refused, naming both versions,
  and nothing is read or written:

  ```text
  [MGS3051] job store ~/.local/state/magus/jobs/.../jobs.jsonl is at safetyscissors.job/v1alpha3,
    newer than this magus knows (safetyscissors.job/v1alpha2): update magus
  ```

- **At a version in the group that no migration converts to**, the store is
  refused, naming the versions this magus knows, and nothing is read or
  written.

Migrations only go up. To go back, run the magus that wrote the store.

A header written before store versions were api versions counts its migrations
in a `migration` member instead. Until the next release, magus reads that count
as the version of the migration it counts to, and the next migration rewrites
the header with its `api_version` and without the count.

A magus built from another contract than its checkout carries never writes the
job store at all: every write is refused with MGS1021, naming the relink.

The header's `api_version` is the store version; each row carries its own, the
record shape it was written in. An older magus still reads a row that carries
members it does not know, and writes them back out on its next write. A
migration changes what an existing member means or is called, which an older
magus would misread, so it refuses the store instead. A magus built before
migrations existed knows no store version; a migration whose rows it would
misread also names a feature in the header's `requires`, which every magus that
lacks the feature refuses.

The job store (`jobs.jsonl`) is the first store that migrates.
