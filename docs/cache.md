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

# Cache model

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

### Per-target inputs and outputs

A spell contributes its globs to *every* target on the project. To attach a glob
to *one* target, declare it in that target's body with `magus.inputs(...)` /
`magus.outputs(...)`:

```buzz
export fun build(args: [str]) > void {
    magus.inputs("schema/**", "codegen.config.json");
    magus.outputs("dist/**");
    go["go-build"]();
}
```

`magus.inputs` adds source globs to that target's cache key; `magus.outputs` adds
output globs to its snapshot/replay set. So a target's footprint is the **union**
of three layers: the bound spells' globs, the project-wide `sources`/`outputs`,
and the target's own `magus.inputs`/`magus.outputs`. Per-target declarations only
ever *add* - they never shrink the project-wide baseline (see
[Granularity](#granularity-project-wide-vs-per-target)).

The globs are read **statically**, before the target runs - a cache hit skips the
body, so the run can't be the source of truth. magus recovers them from the
source: it walks each target body and the helpers it calls by name, collecting the
**string-literal** globs. Two disciplines follow, both enforced:

- A **non-literal argument** (`magus.inputs(someVar)`) is a magusfile load error -
  a computed glob is invisible to the static read, and silently dropping it would
  risk a stale hit.
- A call the walk **can't reach** (in an unreferenced helper, or the identifier
  used as a value) never enters a key; `magus doctor` flags it as
  [MGS1004](codes/magusfile/MGS1004.md).

This shares the literal-first discipline of [`magus.needs`](dependencies.md):
declare the footprint at the target, in literals magus can see.

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

### Opting out and busting

Four controls, at four different scopes:

| Control                         | Scope                       | Semantics                                                                                     |
| -------------------------------- | ---------------------------- | ----------------------------------------------------------------------------------------------- |
| `skip_cache` target policy        | one target, every run         | Always runs; never replays **or** snapshots (a long-running `fs.watch` loop, a service op).      |
| `magus run <target> --no-cache`  | one target, one invocation    | Skips replay for this run only, but still snapshots on success - the entry is refreshed, not left stale, unlike `skip_cache`. |
| `magus.bust_cache(path?)`        | runtime, one magusfile call   | Clears manifests (one project, or the whole cache if `path` is omitted) from inside a target body. An escape hatch that logs a warning every time - the fix is usually to model the missing input as a declared `needs` source instead. |
| `magus clean --cache`           | CLI, whole cache               | Wipes the on-disk store from outside any run.                                                  |
| `cache.immutable` (`MAGUS_CACHE_IMMUTABLE`) | whole cache, whole run | Read-only mode: replays hits, but a miss runs the target and does **not** write a new manifest.  |

`skip_cache` and `--no-cache` both force a genuine re-execution; the difference
is entirely about what happens to the cache entry afterward (never snapshot vs.
snapshot-and-refresh). `bust_cache` and `clean --cache` both delete entries, at
different granularities and from different sides of a run. `cache.immutable` is
the odd one out: it does not force anything to re-run, it just stops the cache
from ever writing - the common case is a read-only CI runner or a shared cache
mirror that must not accumulate local entries.

### Granularity: project-wide vs per-target

A project's `Step.Sources` is not built per-target from scratch: `baseStep`
seeds it with **every bound spell's `needs` globs, unioned, plus the
magusfile itself**, and only then does the target-specific step add that
target's own extra sources on top. So a project binding both `go` and
`docker` has every one of its targets - `build`, `test`, `lint`, even a custom
one - keying on the union of both spells' `needs`, not just the ones relevant
to that particular target: a `Dockerfile` edit invalidates `magus run build`
even though `build` only cares about `.go` files. This is deliberately coarse
(it is a safety margin against under-declared inputs, not a bug), but it means
`magus run ci` and `magus run build` on the same project invalidate together
far more often than their names alone would suggest.

Per-target [`magus.inputs`/`magus.outputs`](#per-target-inputs-and-outputs) does
**not** undo this. It *adds* to a target's footprint; it cannot remove the
project-wide baseline. Declaring `magus.inputs("src/**")` on `build` does not stop
a `Dockerfile` edit from busting it, because the `docker` spell's globs are still
in the baseline. Per-target inputs are for attaching an input a target needs that
nothing else declares - not for narrowing below the spell baseline.

That gives a clean rule for **where to declare a glob**:

- **affects every target** (a shared schema, a project-wide config) -> project-wide
  `magus.project({sources = [...]})`, declared once;
- **affects one target** -> `magus.inputs`/`magus.outputs` in that target's body.

Declaring the same glob in both layers is a no-op (the union already has it) and
`magus doctor` flags it as [MGS1005](codes/magusfile/MGS1005.md). Outputs are
almost always target-specific (`build` -> `dist/`, `test` -> `coverage/`), so a
project-wide `outputs` - which makes *every* target snapshot it - is usually the
wrong tool; prefer per-target `magus.outputs`.

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

## The two roles of an output (maintainer note)

An output glob answers two different questions, and magus keeps them on two
different code paths. Confusing them is the easiest way to introduce a stale-hit
or a broken `magus clean`, so the model is worth stating once.

| Role | Question it answers | Scope | Where it lives |
| ---- | ------------------- | ----- | -------------- |
| **Cache footprint** | "what does *this target* snapshot and replay?" | one target | `cache.Step.Outputs`, assembled per-target in `buildStep`: the project-wide `Outputs` plus that target's `magus.outputs`. |
| **Generated-files manifest** | "what files does *this project* generate?" | whole project | `types.Project.AllOutputs()`: the project-wide `Outputs` unioned with *every* target's `magus.outputs`. |

The cache role is per-target on purpose. A miss snapshots exactly the outputs in
that target's `Step`, and a hit replays exactly those - so an output must be
declared on the target that **produces** it. This is the **producer-ownership
rule**, and violating it is a real bug, not a style nit: a glob declared
project-wide is in *every* cacheable target's `Step.Outputs`, including targets
that never write it. When one of those unrelated targets gets a cache hit, its
replay restores the file to whatever it was when *that* target last ran - so a
`go-build` hit can silently **revert** a freshly regenerated `MAGUS.md`. Scoping
the output to its producer with `magus.outputs` means only the producer's hit
replays it. Project-wide `outputs` is correct only when every target genuinely
produces the glob, which is rare - most outputs belong to one generator.

The generated-files role is the union, because "clean everything this project
generates" and "which project owns this path?" don't care which target produced
what. A consumer that asks a generated-files question goes through `AllOutputs()`,
never raw `p.Outputs`, or it silently misses per-target declarations. Today that
means `magus clean --outputs` (`CleanOutputs`), output-ownership
(`FindOutputOwner`), and the git merge driver (`workspaceOutputGlobs`). The cache
path is the one place that stays per-target.

Inputs have just the one role (the cache key), so there is no `AllInputs`: a
source glob that isn't in a given target's `Step.Sources` simply doesn't key that
target, which is a footprint question, never a "what does the project consume"
one.

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
- [dependencies.md](dependencies.md): how `depends_on`'s `dep:` propagation and a `magus.needs` call each interact with this cache key.
- [operations.md](operations.md): the run hierarchy and the `target.result` event that fires on a hit.
- [targets.md](targets.md): what a Target is - the unit a cache key is computed and replayed for.
- [charms.md](charms.md): the execution modifiers that key into the cache as `charm:` lines.
- [remote-cache.md](remote-cache.md): sharing these artifacts across machines under a signed trust model.
