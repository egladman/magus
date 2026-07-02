# MGS4003: non-deterministic output

A project's declared outputs produced different content across two consecutive
runs of the same dispatch. The same inputs should yield the same outputs; magus
treats determinism as a build-system invariant.

```text
[MGS4003] non-deterministic output (see …/MGS4003.md)
  project=api target=build differing_paths=[api/dist/bundle.js api/dist/manifest.json]
```

## Why

This warning is only emitted by `--race=replay`, which:

1. Runs the affected set normally (concurrent, cache-aware).
2. Content-hashes every declared output of every project.
3. Re-executes the same set sequentially, bypassing the cache.
4. Content-hashes the outputs again.
5. Compares the two hash sets per project.

Any project whose outputs differ between the two runs is non-deterministic.
Common causes:

- **Embedded timestamps.** A build that injects `time.Now()` or `__DATE__`
  into the artifact (linker build IDs, `pkg/build.go` ldflags, source maps with
  generation timestamps).
- **Map iteration order.** Go and Python iterate maps in random order; a
  generator that emits code from a map without sorting keys will produce
  different bytes each run.
- **Parallel work-stealing leaking into output.** Worker IDs, goroutine IDs, or
  the order of fan-in results landing in a manifest.
- **PID / hostname / cwd in the output.** Some toolchains embed these by
  default (e.g. `pip` embeds the build tempdir path into compiled `.pyc`).
- **Random salt / nonce.** Crypto helpers used at build time without a fixed
  seed.

This check is expensive: it doubles the wall-clock time of the affected
build, and the second pass cannot benefit from the cache. Run it for CI
nightly or a manual audit rather than every push.

## Resolution

### 1. Identify the source

The `differing_paths` list narrows the search. Open one of the differing files
in both runs and diff them. The divergence is often visible at first glance
(a timestamp, a permuted list, a tempdir path).

For binary outputs, use `diffoscope` (the reproducible-builds.org tool) which
unpacks archives, decompresses sections, and diffs the contents recursively:

```sh
magus run build
cp -r api/dist /tmp/run1
magus run build --race=replay   # second run rebuilds
diffoscope /tmp/run1/bundle.js api/dist/bundle.js
```

### 2. Remove the source of non-determinism

Common fixes:

- **Embedded timestamps:** ldflag the build time to a fixed value
  (`-X main.BuildTime=$(git log -1 --format=%cI)` ties it to the commit
  timestamp, which is deterministic).
- **Map iteration:** sort keys before emitting (`slices.Sort(maps.Keys(m))`).
- **Tempdir paths:** use a fixed relative path or strip the absolute prefix.
- **Build IDs / PIDs:** pass `-buildid=` to `go build` and `-Wl,--build-id=none`
  to the linker.

### 3. If non-determinism is unavoidable

Some artifacts (e.g. compiler outputs with debug info on Windows) are
non-deterministic by design. For these, exclude the path from the project's
declared outputs so the cache doesn't track them.

## What this is NOT

- **Not a runtime crash detector.** It only checks that outputs are bit-for-bit
  identical, not that they behave the same.
- **Not run by default.** You must pass `--race=replay` explicitly.
- **Not a substitute for `reproducible-builds.org` tooling.** That community
  has stronger guarantees (cross-machine, cross-time, cross-locale). MGS4003 is
  a same-machine same-session check.

## See also

- `MGS4001.md`: runtime filesystem race detector.
- [reproducible-builds.org](https://reproducible-builds.org/): the gold
  standard for build determinism, plus `diffoscope` for inspecting differences.
- `--race=replay` mode on `magus run`.
