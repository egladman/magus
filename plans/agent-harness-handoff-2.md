# Handoff: guard denials, cache invalidation, Buzz parity

Written 2026-07-29. Branch `feat/agent-harness-handoff-92f105`, **25 commits** on
top of `add-install` (which this session fast-forwarded from `48564ef3` to
`27444cd9`). Nothing pushed. Continues `agent-harness-handoff.md`.

## Branch strategy - everything lands on `add-install`

`add-install` is the integration branch, and it already has an **open GitHub PR**.
The plan is: merge locally into `add-install`, push that, let CI run on the PR. It
will be a very large PR, and that is expected and fine.

State as of this handoff:

| ref | commit | note |
| --- | --- | --- |
| `origin/add-install` | `46d36064` | what the PR currently shows |
| `add-install` (local) | `27444cd9` | **60 commits ahead of origin**, unpushed |
| `feat/agent-harness-handoff-92f105` | `152c5317` | +25 on top of local `add-install` |

So the PR gains roughly **85 commits** on the next push. That is the intent, not an
accident - do not try to slim it down.

The merge is a **fast-forward**; this branch is a strict descendant, so there is
nothing to resolve:

```sh
git -C /Users/eli.gladman/Repos/magus checkout add-install
git -C /Users/eli.gladman/Repos/magus merge --ff-only feat/agent-harness-handoff-92f105
```

The other live worktree branches - `feat/cross-project-outputs-handoff-d64695`,
`feat/graph-storage-flatbuffers-bd07c4`, `feat/tty-region-cache-logging-3dbaf9`,
`feat/plans-buzz-parity-handoff-9b8119` - are all **0 commits ahead** of
`add-install`; their work is already folded in. Nothing to merge from them.

**Settle the uncommitted `status.proto` question below BEFORE merging and
pushing** - half-applied generated code is exactly what CI gates on, and it would
fail the PR for a reason unrelated to everything else in it.

Do not push without being asked.

## Start here

```sh
go build -o /tmp/magus ./cmd/magus     # HEAD; the hook resolves this via GUARD_MAGUS_BIN
/tmp/magus run go::go-test .           # green
/tmp/magus run conformance libs/gopherbuzz   # green, 53/83 at pinned ed42f47
```

## UNCOMMITTED, and the first thing to settle

**`status.proto`'s enum rename is half-applied.** `proto/magus/status/v1/status.proto`
renames `QUEUED` -> `STATE_QUEUED` (etc, buf's ENUM_VALUE_PREFIX), but `proto/gen/**`
was never regenerated, so the generated Go still has the old names. That is exactly
the drift CI gates on.

Blocked on a toolchain mismatch: `mise.toml` pins go `1.26.5`, the shell resolves
`1.26.4`, and `protoc-gen-go` asserts they match. `mise install go@1.26.5` already
ran and succeeded; it needs `mise use go` or a fresh shell to take effect. Then:

```sh
/tmp/magus run generate proto
```

...and update the 8 Go references (`internal/handler/status/wire.go`, `wire_test.go`)
plus `console/src/console/dashboard/state.ts`'s `TargetRun_State.QUEUED` map.

Alternative: revert the enum rename, keep `proto/buf.yaml` (independent, lint-green).

Also uncommitted: **64 regenerated `docs/gen` + `console/gen` files**, untouched by
request. They are real drift (stale source line links; `magus\ls`/`affected`/`graph`/
`where` were undocumented), not temporal churn.

## What landed

**Guard: two deny triggers.** The doctrine is now *"magus denies what cannot be
UNDONE, or what has an EXACT WORKING EQUIVALENT."* The second is new and is what
justifies denying a harmless `go test`. It also reframes the reverted grep deny
correctly: that failed for lack of a replacement, not because grep is safe.

- Raw language tools (`go test`, `pytest`, `eslint`, ...) now **deny**. As an
  advisory this fired on every raw `go` call in this session and changed behaviour
  **zero times**.
- `git add -A` / `.` / `-u` now **deny**. One such call swept 69 files into a
  commit about four methods.
- Patterns are anchored to a **command position** (`cmdPos`), because these names
  appear constantly in test data and docs. Three false positives were found by
  dogfooding: a heredoc, an escaped `\|` grep alternation, and `--version`.
  All three are fixed and pinned in `TestEvaluateBashGuard`.

**`MAGUS.md` is for humans.** The skills said "read `MAGUS.md` first"; they now say
it is a generated index, true only as of its last regeneration, and route to live
verbs (`magus describe targets`, `magus ls`). Skill contract v18 -> **v19**,
installed to all four destinations. Left alone where it is correct (`magus-vcs`
and `magus-memory` cite it as an example of generated output).

**Cache invalidation, both failure modes, documented** in `docs/concepts/cache.md`:

- *manifest-as-input* (the Nx pattern) -> nuclear over-invalidation;
- *tools outside the key* -> silent under-invalidation and a debugging rabbit hole;
- and the design point: the declaration lives on the SPELL, so discipline is spent
  once in the adapter rather than repeatedly in every consumer.

**Multi-probe.** `mgs_getVersionCommands() > {str: [str]}` lets one spell probe
several binaries; key entries read `spell:tool:version`. The unnamed probe keeps
its original `spell:version` spelling deliberately - renaming it would rewrite
every key in every workspace. Declared for `hadolint` (docker) and `golangci-lint`
(go); deliberately NOT for buf's protoc plugins (see below).

**golangci-lint runs from PATH**, pinned `aqua:golangci/golangci-lint = 2.12.2`.
It was never in `go.mod`'s tool block, so `go tool golangci-lint` reported
"no such tool" - the op could not run at all. `hadolint` pinned at 2.14.0.

**Two silent-failure bugs fixed:**

- `magus.project`'s `spells` list REPLACED the default binding, dropping the
  magusfile spell, so a project declaring any spell silently stopped dispatching
  its own targets and reported `[pass]`. This repo's `proto` had three no-op
  targets that way, including the `ci` anchor. `magusfile` is now bound implicitly.
- `--` args leaked into `ctx.needs` dependencies, so `magus run test <p> -- -run X`
  handed `-run X` to gofmt. That made the documented way to narrow a run unusable
  on any target with a dependency.

**gopherbuzz parity 48 -> 53** (pinned `ed42f47`): `extern fun`, type values
(`<T>` + a static `typeof`), `??` precedence (upstream puts NullCoalescing between
Term and Bitwise; ours was looser than `or`), map merge, float modulo, collection
`copy*` aliases, `map.hasKey`, `fill` windowing, `remove` returning null.

**Also:** every `ToMap` boundary type now has a Buzz mirror (15); generated Buzz
moved to `internal/spell/gen/types/`; installed skills declared as outputs;
`describe target` reports its own outputs; `describe spells` reports `version_probe`;
`magus ls` uses the declared project name.

## Open, in rough priority

1. **`proto lint` fails** - real buf violations, surfaced once its targets started
   running. `proto/buf.yaml` waives the two response-uniqueness rules with reasoning
   (four trigger RPCs share one response by design). The enum half is the
   uncommitted work above.
2. **`ctx.versions`** - a per-target version probe. `proto`'s codegen runs
   `go tool protoc-gen-go`, pinned in the ROOT `go.mod`; probes are per-SPELL, and
   which plugins a buf project runs is decided by ITS `buf.gen.yaml`, so the
   built-in spell cannot name them. Declaring `../go.mod` an input WAS tried and
   reverted - it is the anti-pattern the docs now warn about. The gap is recorded
   in `proto/magusfile.buzz`. `go tool protoc-gen-go --version` works fine.
3. **`magus vcs add`** - asked for three times, never written up. The idea: stage
   sources, report the generated files skipped and which target wrote them. It
   turns the `git add -A` denial into a replacement rather than a refusal, and it
   sidesteps the guard's text-matching fragility entirely (structured args cannot
   be fooled by prose). Scope it to staging and reverting, not a general proxy.
4. **declared-vs-actual toolchain** - `mise.toml` pins 1.26.5, the machine ran
   1.26.4, nothing compares them. Multi-probe does NOT solve this: it records what
   IS, never what SHOULD be. Doctor-check shaped.
5. **jj/hg guard rules** - the whole-tree denies are git-shaped. jj snapshots the
   working copy and keeps an operation log, so its equivalents are recoverable and
   would NOT meet the deny bar. Deliberately unwritten: neither tool is installed
   here and I would be guessing.
6. **Buzz cleanups now unlocked** - `vcs\describe()` returns `("", nil)` on failure
   (`std/vcs.go:412`, with a `//nolint:nilerr`), so magusfiles compare `== ""` as a
   falsy sentinel. Now that `??` binds correctly, the fix is a nullable host return
   plus `?? "unknown"`. Also newly usable: map merge for option maps, `mut` upvalue
   accumulation, free identifiers for reserved-word keys.
7. **`extern fun` -> typed host modules** - the machinery landed but is unused.
   `import "magus"` still types as `Unknown` because a host module has no source to
   collect signatures from; a generated file of `export extern fun` declarations
   would fix it and remove the `> ExecResult` annotations.
8. **Dogfooding superset** - investigated, recommended AGAINST. The one rule I
   tried to encode turned out to be a declaration gap, not a template gap. Fork the
   templates only for a rule that genuinely cannot be a declaration; none found.

## Corrections worth carrying (I was wrong in-session)

- **"No spell declares a version probe"** - false. I read `describe spells -o json`
  for a field `SpellEntry` did not have, and printed the fallback for all twelve.
  Probes were always declared, asserted by a golden test, and in the key by default.
- **"Local spell ops are a silent no-op"** and **"local spells don't expand under
  `--dry-run`"** - both false, both my fixture omitting the magusfile spell.
- **The MGS1009 story** - I claimed the repeated cache misses were a misattributed
  toolchain signal. With the probe genuinely in the key, that does not hold.
- **"The resolved binary path encodes the version"** - true for this machine's mise
  layout, false in general (shims, `/usr/bin`, Homebrew symlinks). This is why a
  probe CACHE is not safe: its failure mode is a wrong cache HIT, trading away the
  guarantee the probe exists to provide.

The pattern: I trusted an introspection surface or a synthetic fixture instead of
executing the real thing. Where a claim matters, run it.
