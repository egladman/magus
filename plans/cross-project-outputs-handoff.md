# Handoff: cross-project outputs, plus unlanded review findings

Written 2026-07-28. Everything below is **uncommitted** — 371 changed paths on top of
`d371df6b`. Nothing in this session has been committed. Read this before touching
anything; parts of it correct claims made earlier in the session that turned out wrong.

`go build ./...` and `go vet ./...` are clean. `go test ./...` passes except
`TestProductionCodeUsesSharedCodec`, which fails **only** on files under
`.claude/worktrees/` — the stale-worktree gotcha CLAUDE.md documents. Clearing the dead
`graph-storage-flatbuffers-bd07c4` worktree should settle it; it is not from this work.

---

## 1. Cross-project outputs — in flight, blocked on a design decision

### The decision to make first

Do not resume coding until this is settled, because it decides whether the remaining
work is small or should be reverted.

The real invariant in magus is **not** "magusfiles traverse down, never up." It is
**outputs are confined to the declaring project's subtree** — root's subtree just
happens to be the whole workspace. That is why `joinGlob(p.Path, o)` (magus.go:588) can
only descend and why `snapshot.go:117` rejects `..`.

Consequences:

- **Downward cross-project outputs already work.** A root magusfile can declare
  `outputs: ["lib/dist/**"]` today and it snapshots and replays correctly.
  `MGS3001 DescendantBoundaryCrossed` already names a write-mode walk crossing into a
  registered descendant, so writing into another project was considered and guarded.
- **Sideways (sibling) has never been allowed.** The fixture built for this work
  (`producer` → `../site`) is sideways, which is why its runtime write fails.

The recommendation on the table, not yet accepted: **support downward only; do not add
sibling or parent.** Rationale —

1. Containment is load-bearing. `AllOutputs()`, `magus clean`, ownership lookup, the
   merge driver, and the race detector all resolve globs against the declaring
   project's root. Every one needed a same-project special case during this work; that
   is the model objecting, not incidental friction.
2. Upward is worse than sideways: a child writing its parent's tree makes the parent's
   cache key depend on something below it in the hierarchy that defines ownership.
3. **The sibling case likely has a better answer inside the existing model.** For
   `proto` → `docs`, have `proto` write its own tree and have `docs` declare a
   cross-project **input** on it. That already works, already derives ordering, and
   preserves containment. The only reason cross-outputs were reached for is that esbuild
   resolves `@bufbuild/protobuf` from the importing file's location — a JS toolchain
   quirk fixable with `nodePaths`, not a magus gap.

**Test proposition 3 before building anything further.** If it holds, scope collapses to:
make the downward case declarable via `<alias>.file(...)` (most of which has landed), and
reject sideways with a clear error instead of the current confusing one.

### What has landed and is verified

- `types.OutputRef{Project, Glob}` (types/describe.go) — deliberately a **distinct type**
  from `InputRef` despite the identical shape, because the dependency edge each implies
  runs the opposite way. Sharing one type would let a caller pass an input where an
  output belongs and silently invert a build order.
- `TargetGraphNode.Outputs` and `Project.TargetOutputs` now carry `OutputRef`, not
  `string`.
- `internal/describe/extract.go` — the `outputs` branch now recognizes
  `<alias>.file(...)` exactly as the `inputs` branch does. **This alone fixes a real
  bug**: a cross ref previously fell through to `DynamicIO` and raised
  *"requires string-literal globs; a computed argument is invisible to the cache"* for an
  argument that is entirely static. Worth keeping even if the feature is reverted.
- `describe.go` `applyTargetDepsAndFootprint` — resolves output refs to an owning
  project and collects `crossOut [][2]string{owner, writer}` across the whole walk,
  applying edges after it (the owner may not be walked when the writer declares it).
  An output into a path no project owns is skipped, not an error.
- `run.go` buildStep — the outputs fold joins against the **owning** project, mirroring
  the inputs fold.
- `AllOutputs()` (types/project.go), `effectiveOutputs()` (run.go) — deliberately
  **same-project only**, with comments explaining why. Both are contractually
  project-root-relative.
- `internal/graph/knowledge/io.go` — `produces` edges point at the owning tree.
- Consumers updated: `internal/doctor/checks.go` redundant-glob check,
  `types/project_test.go`, `internal/graph/knowledge/io_test.go`.

**Verified working:** the inverted edge derives correctly. With `producer` declaring
`ctx.outputs(site.file("generated.txt"))`, `magus ls` reports:

```
project: site
  depends_on: [producer]
```

That was the piece with genuine design risk (getting the direction backwards would
silently build in the wrong order), and it is correct.

### What does not work

The target body's actual write fails: `sh -c "echo hello > ../site/generated.txt"`
exits 2 under magus. It is **not** the FS sandbox — `MAGUS_SANDBOX_ENABLED=false`
changes nothing — and the identical command succeeds when run by hand from the same
directory. Undiagnosed.

**Start by capturing the subprocess's real stderr**, not its exit code. The failing
target's output ref did not resolve when queried; go straight to the captured log under
`.magus/logs/` or run with `-vv` after the streaming fix (see §2) so output is not
withheld.

Per the design decision above, this may be a write that *should* be rejected — in which
case the work is to make the rejection clear, not to make the write succeed.

### Fixture

Rebuild under a scratch dir (`gen/` is in the default ignore-dirs, so do not name a
project that):

```
xproj/magusfile.buzz            magus\project({});
xproj/producer/magusfile.buzz   import "project/../site" as site;
                                ctx.outputs(site.file("generated.txt"));
                                magus\cmd(["sh","-c","echo hello > ../site/generated.txt"]);
xproj/site/magusfile.buzz       magus\cmd(["sh","-c","cat generated.txt"]);
```

Note the import spelling: `import "project/../site" as site` — the `project/` prefix is
required. This fixture should become the test, asserting both the derived `depends_on`
and the produced file (or the clear rejection).

---

## 2. Review findings not yet fixed

Three review passes (style, nit, skeptic) ran against this session's Go changes. Fixed
already: `hg.go` orphaned godoc, `yield.go` ignoring `sc.Err()`, dead `httpx.Time`, inert
`TimingTransport`, `-vv -q` streaming. The rest, worst first:

### Critical — pre-existing, not from this work

**`cmd/magus/verbosity.go:157` clobbers `Log.Level` unconditionally.**
`--log-level`, `MAGUS_LOG_LEVEL`, and `log.level` in `magus.yaml` are **all dead** —
overwritten by the verbosity-derived level before anything reads them. It looks like it
works because `main.go:305` reads a different variable for the trace retrofit. Fix: guard
the assignment the way `Log.Stream` is guarded three lines below, and derive `lvl` from
`globalCfg.Log.SlogLevel()` when no verbosity flag was passed.

Related, needs verification: `levelName` emits `"INFO"` uppercase, but
`config.Log.Level`'s tag is `validate:"omitempty,oneof=trace debug info warn error"` and
case-sensitive — a `magus config` round-trip may fail validation on next load.

### High

**`run.go` `verifyReadOnly` has no baseline.** It fails on *any* dirty entry, including
files modified before the target ran, and untracked files (`git status --porcelain`
emits `??`, so an editor autosave trips it). So editing a file and running
`magus run generate` accuses `generate` of writing your own edit. Daily false failure on
the command CLAUDE.md names as the pre-commit gate. Fix: snapshot `DirtyFiles` before
`fn` and diff, or scope the post-check to the target's declared outputs.

Two more defects in the same function: it runs the drift check on the ctx
`withTargetDeadline` already bounded (a target that consumes its timeout gets
"could not report working-tree status" instead of the real `DeadlineExceeded`), and it is
scoped to `p.Dir`, so a target writing outside its own project passes the gate having
drifted.

**The `fellBack` hint cannot reach the audience its comment names.** `run.go` claims it
serves the MCP `run_affected` tool, but `interactive.Emit(os.Stderr, ...)` writes to the
daemon's stderr, which the agent never sees. Either carry the fallback on `runResult`
(a `FellBack bool` + reason) or delete the claim. It also fires before the
`len(targets) == 0` check, so a fallback selecting zero projects still announces
"EVERY project was selected".

### Medium

- **`checkCacheYield` returns `StatusFail`**, and `cmd/magus/doctor.go:54` turns any Fail
  into a command error. A performance advisory now makes `magus doctor` exit non-zero —
  it will break CI for anyone whose cache is merely cold. There is no `StatusWarn`. Prior
  art for this exact situation: `internal/doctor/checks_mcp.go:19` ("informational,
  always StatusOK"). Also, it prefixes `Message` with `[MGS1009]` while six sibling
  checks put `see <code>: <url>` in `Details` — and it never emits the URL at all.
- **`invocation.go` `%.0fm` prints "0m spent"** for targets just over threshold
  (8 runs × 2s = 16s → `16000/60000` → `0`). Fix at the type (below), then use `fmtDur`.
- **`Ms`-suffixed ints should be `time.Duration`.** `Stalled.TotalMs`, `AvgMs()`,
  `SlowExecutions(..., minMs int64)`. Every consumer divides back out, and two of them
  picked *different units* — which is how the "0m" bug got in. Convert once in
  `scanJournal`; the `dur_ms` wire field stays.
- **`"project\x00target"` key encoding is leaked** through two exported functions
  (`StalledTargets` takes it, `SlowExecutions` returns it) with no exported constructor.
  Either introduce a `TargetKey` type, or collapse to one exported function with
  `slowExecutions` unexported.
- **`yield.go`'s `only` filter runs post-unmarshal**, so the affordability claim in its
  doc comment is false — a run pays the full parse of 200 journals and then discards.
  Pre-filter on raw bytes or gate on `rec.Kind` first.
- **`Tags` vs `History` asymmetry**: `History` bounds by count, `Tags` is unbounded (a
  5k-tag repo returns 5k). And `Tags`/`Describe` encode "backend lacks this" as empty/""
  while `types.ErrVCSUnsupported` already exists and is used by `RemoteURL`.
- **`vcs/vcs.go` `parseTags` validates the pattern only when output is non-empty**, so a
  malformed glob is silently accepted on an empty result — contradicting its own doc
  comment, and `vcs_test.go` locks in the wrong behavior. Hoist validation above the
  empty check.
- **`vcs/hg.go` `hg tags --template "{tag}\t{date|rfc3339date}\t{node}\n"` is unverified.**
  `hg` is not installed on this machine and there is no `vcs/hg_test.go`. `date` may not
  resolve in the `tags` formatter context. Best case every hg tag gets a zero Date; worst
  case `hg` exits non-zero and `vcs.tags()` raises in an hg repo. Verify against a real
  `hg`; if unavailable, drop to `{tag}\t\t{node}\n` and document it.

### Low / naming

- `httpx.Recorder` collides with `httptest.ResponseRecorder`'s established meaning
  (recording responses, not durations). `Stopwatch` was suggested.
- `Log.Stream` / `IsStream()` — `IsStream` does not parse as English, and "stream" now
  has a third meaning in the tree (`(*Magus).Stream`, `StreamOption`). Suggested:
  `StreamOutput` / `IsStreamingOutput()`, keeping `yaml:"stream"`.
- `MinRunsForYield` / `MinAvgMsForYield` are exported with no consumer outside
  `package cache`; and "yield" appears in no type or user-facing string — the vocabulary
  elsewhere is *stalled* / *never replays*. Unexport and rename.
- `Recorder.Elapsed`/`Calls` nil-guards are unreachable (the only caller constructs it
  non-nil). Keep `Add`'s guard, drop the others.
- `Stalled` → `StalledTarget`; `netAttrs` → `remoteAttrs` (three words for one concept
  across `netAttrs`/`remote_ns`/`remoteSuffix`); attr key `remote_ns` → `remote` (its
  sibling `duration` is also nanoseconds with no suffix).
- `netAttrs` is defined ~870 lines below its two call sites, and appends the optional
  attr **first**, so records read `remote_ns=... project=...`.
- `cache.go`'s `cache.error` path omits `netAttrs`, so a step that burned 40s fetching and
  then failed reports no remote share. `log.go` computes `remote` for every record and
  `printFailure` never receives it.
- `parseTags` parses *and* filters; suggested split into `parseTags(out)` +
  `matchTags(tags, pattern)`, which also removes the error return from the parser and lets
  hg chain its `tip` filter properly (today `tip` is filtered *after* the pattern, so
  `Tags(ctx, dir, "tip")` differs between git and hg).
- `invocation.go` duplicates `filepath.Join(dir, id+".jsonl")` and re-derives
  `resolveCacheDir`; its comment credits `f.Close()` when `fileHandler.Flush()` is what
  makes results readable.
- `internal/doctor` `runner.cacheDir()` ignores `MAGUS_CACHE_DIR` (the root package's
  `resolveCacheDir` honors it), so with that env set `doctor` reports a confident false
  "no target is running uncached".
- `Tag.ToMap` is untested; `types/hostrecords_test.go` whole-struct-asserts four sibling
  `ToMap`s. `Tag` lacks `Short` (its sibling `Commit` has it).
- `internal/httpx/timing.go`'s 10-line rationale block is free-floating, attached to no
  declaration, so `go doc` shows none of it. Its best line —
  *"magus's time versus your toolchain's time, never network versus compute"* — should be
  package doc.
- The same WHY (stream is not derived from level) is written three times:
  `verbosity.go`, `config.go`, `magus.go`. Keep the canonical one on the field.

---

## 3. Other loose ends from the session

- **`docs` project split — Option A landed.** The `es` plugin moved to
  `proto/buf.gen.yaml` emitting into `../docs/src/gen`; `docs/buf.gen.yaml` is deleted and
  `docs` no longer lists `../proto/**` in sources. Verified: output regenerates
  byte-identical, and a markdown edit leaves `proto generate` **cached** (no BSR request).
  Note this write is *undeclared* and safe only because the artifact is committed.
- **`libs/gopherbuzz/diagnostics.go`'s `bzzDocsBase`** still points at
  `blob/main/libs/gopherbuzz/docs/codes/`. MGS bases were moved to the rendered docs site
  this session; BZZ was left alone because gopherbuzz is a separate module and may not
  publish there. Decide and make consistent.
- **`libs/gopherbuzz/bzz_codes_test.go`** is the one unpaired test file that could not be
  folded: it is `package buzz` and needs unexported `allBZZCodes`/`typeError`, while
  `diagnostics_test.go` is `package buzz_test`.
- **Docs still to write** (blocked on §1's decision): how to declare cross-project
  dependencies, with a **fixture workspace** under `docs/` whose mermaid graph is
  generated at docs-build time. Do **not** commit `magus graph deps -o mermaid` output
  directly — it carries `~1m` forecast annotations that are stable here only because this
  machine has no `history.json`, and would drift per-machine. A fresh fixture has no
  history, so it is deterministic by construction.
- **Tour examples were ruled out** for cross-project anything: all 17 steps are a single
  `.buzz` file = one project, and the format cannot express two.
