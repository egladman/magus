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
SQL database does. Each change is a migration: an ordered step from one version
to the next that rewrites one entry at a time. The store knows its migrations in
order, and its version is how many of them it has been through.

The version lives in the store's own header line, as the `migration` member:

```jsonl
{"api_version":"magus.job/v1alpha1","migration":1}
{"id":"docs-index","state":"pass","criteria":"index the docs"}
```

A header without the member is at version 0.

When magus opens a store:

- **At its version**, the store is read as it is.
- **Older**, magus takes the store's lock and applies every migration past the
  header's version, in order, to every line. It writes the result to a
  temporary file beside the store and renames it into place, so a reader sees
  the old file or the migrated one, never half of either. A migration that fails
  leaves the old file as it was. magus then says what it did:

  ```text
  job: migrated job store ~/.local/state/magus/jobs/.../jobs.jsonl from 0 to 1 (14 entries)
  ```

  Each store migrates once. The next magus to open it finds it current, and
  no reader keeps the old shape.

- **Newer** than this magus knows, the store is refused, naming both versions,
  and nothing is read or written:

  ```text
  [MGS3051] job store ~/.local/state/magus/jobs/.../jobs.jsonl is at migration 3,
    newer than this magus knows (1): update magus
  ```

Migrations only go up. To go back, run the magus that wrote the store.

`migration` is separate from `api_version`. `api_version` records the
newest row shape any writer used, and an older magus still reads those rows,
carrying the members it does not know back out on its next write. A migration
changes what an existing member means or is called, which an older magus would
misread, so it refuses the store instead. A magus built before migrations
existed does not know the `migration` member; a migration whose rows it would
misread also names a feature in the header's `requires`, which every magus that
lacks the feature refuses.

The job store (`jobs.jsonl`) is the first store that migrates.
