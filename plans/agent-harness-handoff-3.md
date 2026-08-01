# Handoff: locks, workspace roots, and CLI output coverage

Written 2026-07-29. Continues `agent-harness-handoff-2.md`.

## Where the work is

Worktree `.claude/worktrees/agent-harness-handoff-92f105`, branch
`feat/agent-harness-handoff-92f105`. **26 commits** on top of `152c5317`, which is
where handoff-2 left the branch.

Everything still lands on `add-install`, the integration branch with the open PR.
Nothing in this session changed that plan.

| ref | commit | note |
| --- | --- | --- |
| `origin/add-install` | `46d36064` | what the PR shows |
| `add-install` (local) | `27444cd9` | unpushed, ahead of origin |
| `feat/agent-harness-handoff-92f105` | `bee01082` | **+51 vs local `add-install`**, 0 behind |

Still a clean fast-forward:

```sh
git -C /Users/eli.gladman/Repos/magus checkout add-install
git -C /Users/eli.gladman/Repos/magus merge --ff-only feat/agent-harness-handoff-92f105
```

Nothing has been pushed. Do not push without being asked.

## Start here

```sh
cd .claude/worktrees/agent-harness-handoff-92f105
go build -o /tmp/magus ./cmd/magus
```

**The toolchain trap, which cost hours last session.** `which go` resolves a stale
1.26.4 while `mise` injects `GOROOT` for the 1.26.5 the repo pins, and the mismatch
surfaces as a confusing `compile: version does not match` deep inside a spell. Every
command below is prefixed accordingly:

```sh
mise exec -- env -u GOROOT go test -race ./...
mise exec -- env -u GOROOT /tmp/magus <cmd>
```

`mise exec` alone is not enough - it sets the mismatched `GOROOT`, so `env -u GOROOT`
is the load-bearing half. `magus describe spells go --versions` now prints the actual
resolved versions, which is the fastest way to confirm the environment before
debugging anything else.

## What landed

**A repo-wide `affected` outage.** `magus affected` failed on every branch, including
`add-install`, so the CI gate CLAUDE.md prescribes could not run at all. `file.Resolve`
read a BARE path as workspace-relative, but an `import "project/<path>"` is always
dot-relative to the importing magusfile, so every descendant import mis-anchored.
Fixed via a named entry point per surface (`ResolveImport` / `ResolveDependsOn` /
`ResolveProject` over an unexported `resolveAmbiguous`), plus **MGS1015** so a
cross-dep that cannot resolve is a load error instead of a silently dropped edge.

**Workspace root detection.** A magusfile marked a PROJECT but was treated as a
workspace root, so running from a subproject silently redefined the workspace. Now
`magus.yaml` is the only workspace marker and NEAREST wins. Two traps found by testing,
both worth keeping in mind:

- listing `go.mod` as a workspace marker reintroduces the bug one level down - this
  repo has `libs/diag/go.mod` and `libs/gopherbuzz/go.mod`, and a submodule that
  becomes its own workspace also gets its own lock and cache namespace, so it stops
  excluding the root run despite touching the same files;
- outermost-wins breaks worktrees, which nest inside their parent repo.

**Services were shared across workspaces.** `Fingerprint` covered only image/tag/
ports/env/volumes, with no workspace component, and the daemon hosts services for
every workspace on the machine - so two checkouts declaring the same `postgres:16` got
the SAME container. `identity.InstanceKey(root, svc)` scopes it.

**Workspace-local Buzz spells lost half their contract.** `loadBuzzSpell` built the
spell without `WithVersionProbe`, `WithVersionProbeNamed`, `WithLanguage` or
`WithOpaque` while the descriptor carried all of them. A declared version probe never
ran and never entered the cache key, so a local spell's toolchain could drift with
nothing invalidating.

**Lock observability**, prompted by a six-day orphaned `magus run serve` holding a lock
in a deleted worktree. `flock` carries no identity, so a blocked run could only say
"another magus process". Now: an owner sidecar naming the holder, waiter markers, a
watchdog that releases when the workspace root disappears, and the state surfaced in
three places (sticky terminal region, `magus status`, console dashboard tile).

**Doctor** gained a stale-worktree check (the MGS1002 trap was documented and never
enforced) and a spell-contract coverage report.

## Open, in rough priority

1. **The txtar grind.** 25 scripts exist; roughly 20 commands still have none:
   `path`, `insight`, `refs`, `clean`, `config`, `tail`, `watch`, `x`, `self`,
   `server`, `man`, `completion`, `buzz`, `repl`, `affected`, `run`, `agent`, `init`,
   `merge-driver`. `doctor` and `status` are partially covered by the format matrix.
   **Write these by hand, one per command.** A programmatic enumerator was proposed
   and explicitly rejected: these are behavioural facts that do not drift, and the
   point is to write them once.
   - Several need care rather than a template: `tail` refuses without a TTY;
     `repl`/`watch`/`server` are long-running; `x`/`run` execute real subprocesses.
     Use the pattern from `spell_local_optional_contract.txtar`, whose fixture spell
     probes `echo` so the test asserts magus rather than the host's installed tools.
   - Write assertions against OBSERVED output. Several of mine failed first because I
     guessed at a shape.
2. **Widen `-o` support.** More commands accepting `-o` is a stated priority: agents
   benefit from being able to assume any command's output can be reshaped. The survey
   found `explain`, `query`, `graph stats`, `where`, `version`, `ls`, `status` and
   `doctor` already support it, so the remaining gap is smaller than it looks - audit
   the list above and add it where missing.
3. **`magus affected ci` is still red**, but NOT from this work. Verified against a
   pristine `152c5317` worktree: root `ci` already failed there at the drift gate,
   because the generated files are uncommitted. The other failures are the
   pre-existing lint findings CLAUDE.md documents (`internal/cache/yield.go`,
   `internal/describe/extract.go`, `cmd/magus-utils/bindings.go`,
   `internal/cache/notice.go`). Settle the generated output and this clears.
4. **Carried over from handoff-2, untouched:** `ctx.versions` (a per-target version
   probe), `magus vcs add`, the declared-vs-actual toolchain doctor check, the Buzz
   cleanups unlocked by the `??` fix, and `extern fun` -> typed host modules.
5. **Cross-project restructure**, deferred deliberately. `console/src/gen` and
   `docs/src/gen` are per-consumer copies of one generated client. The cross-project
   plan wants a single tree plus esbuild `nodePaths` instead. `affected --impact`
   works again, so the before/after can finally be measured rather than argued.
6. **`internal/serviceident` was moved** to `internal/service/identity`. If anything
   external still refers to the old path, that is why.

## Uncommitted

~75 paths, all generated (`docs/gen`, `console/gen`, `MAGUS.md`, `gen/*.json`,
`docs/src/gen`, `console/src/gen`) plus two docs pages regenerated in passing. Left
alone by request. They are the reason the drift gate fails; committing them is the
first step to a green `ci`.

## Corrections worth carrying

- **`describe -o template` is NOT broken.** I claimed it was. Templates bind to JSON
  TAG names (`.name`, `.cache_key`), which is `jsonShape`'s documented contract; I had
  written Go field names. The real defect was that a misspelled key rendered
  `<no value>` and exited 0. `missingkey=error` is now on and the error names the
  lookup. Keep the json-tag binding: `-o json` is the reference a template is authored
  against, and Go identifiers appear in no other format.
- **The orphaned `serve` did not cause the `generate` stall** I blamed it for. It held
  locks in a DIFFERENT worktree. It was a real problem - it explained the
  `libs/gopherbuzz` test failure via the stale-worktree gotcha - but not that one.
- **A doctor check cannot catch declaration-vs-application drift.** Doctor only sees
  the REGISTERED spell, where a dropped hook is indistinguishable from one never
  declared. That is why `TestSpellOptionsApplied` is a test over magus's own
  construction paths, not a workspace diagnostic.
- **Mutation-test your guards.** `TestSpellOptionsApplied` initially could not fail:
  `WithVersionProbe` is a substring of `WithVersionProbeNamed`. Matching on
  `WithVersionProbe(` fixed it. Deleting the option and confirming the test goes red is
  cheap and caught the test lying about its own coverage.

The pattern, again: where a claim matters, run it. Three of this session's wrong turns
were confident assertions that one command would have falsified.
