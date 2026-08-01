# Handoff: typed returns, agent-skill variants, and artifact history

Written 2026-07-30. Continues `generator-layout-handoff.md`, whose five open items
are all closed.

## Where the work is

Worktree `.claude/worktrees/generator-layout-handoff-c30833`, branch
`feat/generator-layout-handoff-c30833`. **24 commits** on top of `33eb75f5`, where
`boop-b26363` left off. Tree clean, `go test ./...` green, nothing pushed.

```sh
cd .claude/worktrees/generator-layout-handoff-c30833
mise trust                      # a fresh worktree's mise.toml is untrusted
mise exec -- env -u GOROOT go build -o /tmp/magus ./cmd/magus
```

The toolchain trap is unchanged from the last three handoffs: `which go` resolves a
stale toolchain while mise injects a `GOROOT` for the pinned one. `env -u GOROOT` is
the load-bearing half of every command here.

## What landed

**The plan's five items.** `internal/generate` to 100% coverage (with two properties
asserted: `emit.Region` idempotence, `godecl` stability under gofmt). Config
generator moved to `internal/config/generate` with byte-identical output. Docs footer
keyed to each page's own last content change, killing churn on 132 of 163 pages.
Ten review findings from the previous handoff. And `internal/` groupings - see the
correction below, because most of that item said no.

**`magus agent install --simple`.** A second curated permutation of every skill from
ONE body, with `<!-- why -->` markers bracketing what the short form withholds. 14%
smaller across the eight skills (6% to 28%). Both forms share one content digest so
they version together; the stamp records `skill-variant`. Docs on the site, the
convention in `magus-skill-authoring`, and a generated showcase page per skill
showing both forms with a Pico `<progress>` size bar (arithmetic in Buzz, in
`docs/engine/meta.buzz`).

**`magus repl` got a line editor.** x/term (already a dependency), so history, line
editing, and Tab completion drawn from four surfaces magus already has: meta
commands, session globals, host modules AND their methods (`fs.writeF<TAB>`), and
workspace targets/projects. Piped sessions are byte-identical - verified, zero escape
sequences. Plus a one-row sticky footer showing language, cwd, and continuation depth.

**Shell completion reads real targets.** All four dialects took eight hardcoded
names; this workspace declares 47. zsh and fish also show each target's kind.

**Streaming shard fingerprint (schema v7).** Replaced marshal-then-hash: -44% sec/op,
-23% B/op, -41% allocs. The bigger win is decoupling the fingerprint from the storage
format, without which a later format change would silently invalidate every cached
shard.

**Host record returns are named.** `magus\cmd(args, [opts]) -> ExecResult` where it
read `map[string]any`. Nineteen methods across nine modules. Codegen validates the
declared name against the reflected Impl and fails on a mismatch, so it cannot drift.

**Artifact history and diff.** `--then file <p> history | diff | hash`, reading the
content-addressed store that already held every past run's artifacts.

## Open work, in priority order

### 1. The `ArtifactVersion` -> `File` decomposition

The review and Eli independently landed on the same point and it is not yet done.
`ArtifactVersion` now embeds `OutputRecord` (that closed a real bug), but it still
fuses two things:

| fields | what it is |
| --- | --- |
| `Output` (path, blob, size, mode, symlink) | the **file** - and what a returnable `File` would carry |
| `Target`, `CreatedAt`, `EntryHash` | the **run** - already public as `ref1a2b3c` |

`Cache.LatestRef(cacheKey)` maps an entry hash to an output ref, so run identity
already exists and `EntryHash` is a private restatement of it. The S3 lesson is the
ADDRESSING, not the type count: the object is the type, the version is a coordinate.

Do this before any proto work - it is the shape an RPC would freeze.

### 2. Generation selection

`diff` hardcodes "the newest version whose bytes differ". There is no way to say
last-last. Wanted:

```sh
magus run build --then file dist/app diff --against ref1a2b3c
magus run build --then file dist/app export --at ref1a2b3c
```

The coordinate should be the output ref, not an index: an index shifts under you when
a new run lands. `--against 2` is sugar on top. No "generations" concept exists in
`internal/jobs` despite a recollection that it did - the word appears nowhere in
jobs, protos, or types. The cache manifests ARE generations in all but name.

### 3. Proto + WebUI for history/diff

Not started, deliberately. Measured cost: a proto service (8 handler packages exist,
~125 lines / 5 RPCs each), buf regen in Go AND the console's TS client, and two gates
that apply to every RPC - the fail-closed Connect audit interceptor with its
arch-test, and buf-breaking. Plus a diff viewer is a real PatternFly component
(read `console/PATTERNFLY.md` first), and the console's service worker serves stale
bundles, so verifying means a fresh port or cleared caches.

The Go side it would call is done and exported: `magus.ArtifactHistory`,
`magus.MaterializeArtifact`.

### 4. Review findings not yet acted on

The three-lens `go-review-ultra` pass produced more than was fixed. Fixed: the fence
regression, the symlink hole, `errNoCache`, missing `ctx`, the impossible error
return, the `.<language>` completion lie, and a batch of renames. **Still open**,
roughly by severity:

- **`magus.pry()` under the raw-mode REPL hangs unkillably.** `Pry` reads
  `bufio.Scanner` over bare `os.Stdin` while `replInput` has put the terminal in raw
  mode: `ISIG` is cleared so Ctrl-C is swallowed, and `\r` never satisfies
  `ScanLines`. Needs verification, then either restore cooked mode around a nested
  Pry or route Pry through `replInput`. Reported high.
- **`git diff` exit 128 reported as success.** `chainFileDiff` treats every
  `*exec.ExitError` as "they differ"; only exit 1 means that.
- **`Region.reflow` can leave DECSTBM margins set with `open=false`**, so `Release`
  early-returns and the user's shell keeps scrolling only its top rows. Also
  re-Reserves under stale margins, which strands old region text in the scroll area.
- **`height == 1` status/failure row collision** in `Region` - the REPL footer uses
  height 1, so any future `WriteLine` there overwrites the status row.
- **Orphan `<!-- /why -->` before any opener** is neither detected nor stripped, so it
  reaches an installed skill.
- **`WriteSkillTree` does not reject `..`**, only absolute paths and `~`.
- **Config codegen silently drops** embedded struct fields and the second name in
  `A, B string`; a backslash in a doc comment breaks the generated file.
- **Export verbs still accept-and-ignore** trailing args (`--then outputs export
  --path ./d -o json` drops the `-o`). The `history`/`diff`/`contents` branch rejects
  it; `chainPathFlag` does not. Same defect, same file.
- **Dead code**: `Catalog.SkillNames` and `Catalog.Section` have zero callers;
  `SkillBytes` and `WellKnownSkillDirs` are test-only; `flagTmplData` and `lookupTag`
  are single-use wrappers. ~75-90 lines.
- **Comment volume.** The strongest cross-cutting critique: seven files open with
  15-45 lines per symbol, in three flavours - archaeology ("an earlier version..."),
  advocacy (arguing the design is right), and restatement. The invariants in there are
  worth keeping and each is one or two sentences. This is a real habit to correct, not
  a nit.

### 5. Unpaired test files

12 mechanical folds done. **20 remain**, each needing a per-file call about which
source it pairs with: 6 benchmarks whose subject is a differently-named source, and 14
arbitrarily named (`script_test.go`, `findroot_test.go`, ...). Deliberately excluded:
6 `example_test.go` (godoc convention) and 3 platform-suffixed files (the suffix IS
the build constraint). `internal/spell/charm_parity_test.go` is the documented
external-package exception - it needs unexported `std` internals AND `host`, which
imports `std`, so every layer up is a cycle.

## Corrections worth carrying

- **Three of the previous handoff's four `internal/` groupings do not survive
  verification.** Its "best candidate" (audit + serviceaudit + trail + journal -> one
  family) has DISJOINT consumers, and `trail` (8 importers) and `journal` (10) are
  shared infrastructure. The daemon grouping was premised on `jobs` and `maintenance`
  serving the daemon's scheduler - `internal/daemon` imports NEITHER. Applied
  honestly, the plan's own rule ("group by who depends on whom") rejects most of the
  list it appears in. Only `quantile` survived: 43 -> 42 dirs.
- **`Region` held the terminal's SINGLE cursor-save register for a whole session**, so
  no repaint could save the cursor around its own write. That was breaking the cache
  handler too (its scrolling `printf` landed in the footer after a status repaint), not
  just the REPL. The contract is now "the caller's cursor never moves", and
  `ReturnCursor` - a primitive I added to work around it - was deleted, because a
  transparent paint needs no caller to put the cursor back.
- **A benchmark caught an 11-fold alloc regression** in the fingerprint rewrite:
  `io.WriteString` on a `hash.Hash` allocates per call, since it is not a
  `StringWriter`. The first version measured -13% sec/op with allocs up 1013%.
- **`skill-variant` is a stamp field, not a skill.** It sits beside
  `agent-skill-version` in each installed SKILL.md's metadata. Still exactly 8 skills;
  the authoring convention went into the hand-authored `magus-skill-authoring`.
- **Generated docs land in two waves.** The site manifest, search index, and service
  worker are written AFTER the pages, so committing right when the pages appear leaves
  HEAD self-inconsistent. Wait for the run to exit, then commit. This bit twice.
- **The repl footer is unverified visually.** Its invariant is unit-tested and the
  piped path is byte-clean, but no pty harness worked here. Two minutes in a real
  terminal would close it.
- **A subagent's finding needs verifying before acting.** All the high findings above
  reproduced exactly; the fence one was verifiable in committed output in one grep.
  That is the third handoff in a row to record this, and it paid again.
