---
title: Cache model
description: How magus computes a content-addressed cache key from a target's declared inputs, replays outputs on a hit without rerunning the body, and stores it all as plain files under .magus.
tags:
  [
    cache,
    needs,
    provides,
    claims,
    cache-key,
    invalidation,
    replay,
    content-addressed,
  ]
---

# The magus cache model

magus's build cache is **content-addressed**: a target's outputs are keyed by the
SHA-256 of its inputs, so an unchanged target replays its previous outputs instead
of rerunning. This page is the **local** model: what magus hashes, what invalidates
a key, what "replay" restores, and where it all lives on disk. The [remote
cache](remote-cache.md) shares these same artifacts across machines and layers a
signed trust model on top; this page is the substrate it references, so we describe
it once here and link there for the distributed story.

## Design intent

- **Correctness is a declaration contract.** magus caches what a target _declares_,
  not what it _touches_. A target's `needs`, `provides`, and `claims` (see below)
  define its whole cache footprint. Under-declare an input and a stale hit slips
  through; over-declare an output and every replay snapshots more than necessary.
  The cache is only as correct as those declarations, which is why the vocabulary
  is explicit rather than inferred from a traced filesystem.
- **Identical inputs replay.** The key is a pure function of the inputs. Two runs
  with byte-identical sources, tool versions, charms, and dependency keys produce
  the same key and so the same hit. Nothing about wall-clock time, machine, or run
  order enters the key.
- **A hit never runs the body.** On a hit magus restores the recorded outputs and
  emits the result event; the target's `export fun` never executes. The saved work
  _is_ the point.
- **It is just files.** The store is a directory of blobs, JSON manifests, and
  captured logs under `.magus/`. There is no database and no daemon in the read
  path. You can `ls` it, `cat` a manifest, and reason about a hit or miss with
  ordinary tools.

## needs, provides, claims: a target's cache footprint

A bound [spell](spells.md) contributes three glob sets to its project. Only
operations are runnable; these three are metadata that make caching and the
affected set correct (see [What a spell provides](spells.md#what-a-spell-provides)).
Binding a spell contributes its `needs`/`provides`/`claims` to a project's cache
key and affected set even before you wire a target.

| Declaration    | What it is                | Role in the cache                                                  |
| -------------- | ------------------------- | ------------------------------------------------------------------ |
| **`needs`**    | input globs (the sources) | hashed into the cache key; also seed the affected set              |
| **`provides`** | output globs              | snapshotted into the cache on a miss and replayed on a hit         |
| **`claims`**   | files the spell owns      | affected-set attribution only; **not** hashed, **not** snapshotted |

Internally these map to a `Step` the cache hashes and replays: `needs` become
`Step.Sources`, `provides` become `Step.Outputs`. `claims` do not appear in the
`Step` at all: they attribute changed files to a project for affected-set
computation and never touch the cache key or the snapshot. Two rules follow
directly:

- **Declare every input in `needs`.** A source file that isn't matched by a `needs`
  glob doesn't enter the key, so editing it produces no miss and you replay a stale
  build.
- **Keep `provides` tight and complete.** Under-declare and the cache can't replay
  an output it never recorded; over-declare and every hit restores files that
  were never outputs.

The output tree is never treated as an input: source expansion excludes the
`provides` globs and prunes their static directory prefixes, so a generated file
can't feed back into its own key.

## The cache key

The key is the hex SHA-256 of a deterministic, newline-delimited serialization of
the `Step`. magus writes these lines, in this order, into one hash:

- **`keyVersion`** - an internal schema version. Bumping it (when the set of hashed
  fields changes) forces a global rebuild.
- **`projectPath`** and **`target`** - so the same sources under different targets
  key separately.
- **`charm:` lines** - the active [charms](charms.md), sorted by name. A
  charm-variant run (`lint:rw`) hashes differently from the bare run, because the
  charm changes behavior. Empty charms add nothing, so charm-less runs are
  unaffected.
- **`src:` lines** - for every file matched by `needs`, its workspace-relative path,
  its content SHA-256, and its executable bit. Files are discovered by a single
  walk, sorted by path, and hashed in parallel. Only the executable bit of the mode
  is folded in (not the full permissions, which would differ across machines with
  different umasks), so `chmod +x` on a script - which changes no content -
  still invalidates the key.
- **`env:` lines** - each allow-listed environment variable name and its value,
  sorted, distinguishing unset from set-to-empty. A variable's value contributes to
  the key only if the spell opted it in.
- **`dep:` lines** - the resolved cache keys of upstream dependencies, sorted. This
  is how a change ripples: a dependency's new key becomes an input line here, so a
  dependent misses transitively.
- **`spellDefVersion`** - a binary fingerprint of the spell definition, so a magus
  upgrade that changes a spell forces a miss.
- **`tool:` lines** - `spell:version` strings, sorted, so a toolchain upgrade
  (a new `go` or `prettier`) invalidates the key even when no source changed.

Because the serialization is stable and sorted, the key is reproducible: identical
inputs anywhere yield the identical key. A `src` file's content hash uses an
mtime + size fast path (a per-file memo persisted under the cache dir), so an
unchanged tree re-keys without re-reading every byte; the memo is a performance
cache for the hash, never a substitute for it.

## Invalidation: what busts a key

A miss is "no manifest stored under this key." Anything that changes a
hashed line above yields a new key, and thus a new (empty) slot:

- editing, adding, or removing a file matched by `needs`;
- toggling the executable bit on a needed file;
- changing the value of an allow-listed env var (or setting/unsetting it);
- an upstream dependency's key changing (transitive invalidation);
- a spell definition change (`spellDefVersion`) or a tool-version bump;
- applying or dropping a charm;
- renaming the project or target.

What does **not** invalidate: a file's mtime alone (content is what's hashed), a
`claims`-only file, or anything outside the declared `needs`.

Old keys are never mutated - a miss writes a _new_ entry beside the old one - so
invalidation is additive. Reverting a change restores the earlier key and replays
its still-present entry. Disk is reclaimed separately by eviction and pruning (see
[On disk](#on-disk-just-files)).

For targets that must never cache - a long-running `fs.watch` loop, a service op -
the step is marked no-cache: magus skips the replay path so they always run, and
skips the snapshot so a re-run re-executes instead of replaying a stale success.

## Replay: a hit restores outputs, not execution

On a run, magus computes the key, then looks for a manifest stored under it:

1. **Hit.** The manifest is read and its outputs are restored into the workspace.
   The target's body **does not run** - the `export fun` never executes on a hit.
   Each output is materialized from the content-addressed store by reflink (a
   copy-on-write clone) where the filesystem supports it, falling back to a byte
   copy. (Hard-linking is deliberately avoided: it would alias the shared blob and
   a later in-place rewrite would silently poison the cache.) Symlink outputs are
   restored as symlinks. Any captured build log recorded on the original run is
   replayed to stdout, so a cached pass looks like the real one.
2. **Miss.** The body runs. On success, magus **snapshots** the `provides` outputs:
   each file's content is hashed, its bytes are stored once in the content-addressed
   store (deduplicated by hash), and a manifest is written atomically recording
   every output's path, content hash, mode, and size. A subsequent identical run
   hits.

This is why the target result is _emitted, not returned_. A return value can't
exist on a hit, since the body never ran - but a hit is exactly what you most want
to report. So the dispatcher emits a **`target.result`** event
(`{project, target, status, cache_hit, duration_ms}`) for **both** the ran and the
cached case, sourced from the cache's per-run callback. See
[Results](operations.md#results-what-each-layer-produces) for how the event fits
the run hierarchy.

A run that "wins the race" against a cancellation is neither snapshotted nor
published: its outputs may be incomplete, so magus surfaces the cancellation
instead of recording a poisoned entry.

## On disk: just files

The cache lives at **`.magus/`** in the workspace root (override with
`MAGUS_CACHE_DIR`, or `cache.dir` in `magus.yaml`). Its layout is three
directories plus a hash memo:

```text
.magus/
├── cas/         content-addressed blobs, sharded by the first two hex chars
│   └── ab/ab34...f0      one file per unique output content (deduplicated)
├── manifests/   one JSON manifest per cache entry
│   └── api/<key>.json     project path flattened; file named by cache key
├── logs/        captured build output, replayed on a hit
│   └── api/<key>.log
└── mtimes/      the per-file hash fast-path memo
```

A **manifest** is plain JSON you can read directly. It records the project path, the
cache key, the target, and one record per output - path, content-address (`blob`),
mode, size, and (for symlinks) the link target:

```json
{
  "projectPath": "api",
  "hash": "ab34...f0",
  "target": "build",
  "outputs": [
    {
      "path": "api/dist/server.js",
      "blob": "9c1f...",
      "mode": 420,
      "size": 20481
    }
  ],
  "createdAt": "2026-07-07T12:00:00Z"
}
```

That transparency is deliberate: a hit or miss is answerable with `ls` and `cat`,
and a build log for any entry is a file you can open. magus never mutates an
existing manifest, and a manifest read back under the wrong key or project (copied
or renamed onto the wrong slot) is treated as a miss rather than trusted.

Space is bounded two ways. An optional size cap (`cache.size_mb` /
`MAGUS_CACHE_SIZE_MB`) drives **LRU eviction** of the oldest manifests after a
build, and orphaned blobs are garbage-collected once no surviving manifest
references them (blobs are shared, so a blob's bytes are only reclaimed when its
last referencing manifest is evicted). Out of band, `magus config cache prune`
evicts entries older than a cutoff. To force a clean rebuild of specific projects,
`magus clean --cache <project>` drops their entries. The whole store is portable:
`magus config cache export` / `import` move it as a gzip-tar.

## Connecting to the remote cache

Everything above is local to one machine. A [remote cache](remote-cache.md) shares
these exact artifacts across CI runners: on a **local** miss magus asks the remote
backend for the artifact keyed by the same `(projectPath, hash)`, and if found
imports it into the local store so the ordinary hit path replays it - no rebuild.
After a genuine build, magus uploads the artifact so the next machine hits.

The artifact is the same content: the manifest, its blobs, and the build log,
packed as a gzip-tar. The key computation, the replay path, and the manifest format
are identical - the remote layer only moves those bytes between machines. On top of
that it adds a **signed trust model**: because a replayed artifact injects files
into a consumer's build, every remote artifact is verified against an Ed25519 trust
set before it is allowed to replay, and an unsigned or untrusted one falls back to a
local build. That trust boundary, the backend contract, and CI wiring are covered
in full in [remote-cache.md](remote-cache.md); this page's model is what it builds
on.

## Glossary

| Term                  | Definition                                                                                                                        |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| **needs**             | A spell's declared input globs. Hashed into the cache key (`Step.Sources`); also seed the affected set.                           |
| **provides**          | A spell's declared output globs. Snapshotted on a miss and replayed on a hit (`Step.Outputs`).                                    |
| **claims**            | Files a spell owns, for affected-set attribution only. Never hashed and never snapshotted.                                        |
| **Cache key**         | The hex SHA-256 of the serialized `Step`: sources, env, deps, tool versions, spell version, charms, project, and target.          |
| **Content-addressed** | Stored by content hash: identical output bytes are stored once, and a blob's name is its own SHA-256.                             |
| **Manifest**          | The JSON record of one cache entry: project, key, target, and one record (path, blob, mode, size, symlink) per output.            |
| **Blob**              | One unique output content, stored once under `cas/`, sharded by the first two hex chars of its hash.                              |
| **Replay**            | Restoring a manifest's outputs on a hit (reflink then copy) without running the target body.                                      |
| **Snapshot**          | Recording a miss's outputs into the store and writing its manifest.                                                               |
| **target.result**     | The emitted report event for one target run (`{project, target, status, cache_hit, duration_ms}`); fires on both hits and misses. |
| **`.magus/`**         | The on-disk cache in the workspace root: `cas/` + `manifests/` + `logs/` + the mtime memo.                                        |

## See also

- [spells.md](spells.md): where `needs`/`provides`/`claims` are declared, and what a bound spell contributes.
- [operations.md](operations.md): the run hierarchy and the `target.result` event that fires on a hit.
- [targets.md](targets.md): what a Target is - the unit a cache key is computed and replayed for.
- [charms.md](charms.md): the execution modifiers that key into the cache as `charm:` lines.
- [remote-cache.md](remote-cache.md): sharing these artifacts across machines under a signed trust model.
