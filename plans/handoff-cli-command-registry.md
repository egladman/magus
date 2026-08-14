# Handoff: the CLI command registry

Written 2026-08-14. Everything below is on ONE branch and ONE worktree; keep
working there and keep pushing there.

- Branch: `improve-terminal` ([PR #52](https://github.com/egladman/magus/pull/52))
- Worktree: `.claude/worktrees/improve-terminal-insight`
- 63 commits ahead of `origin/main`, based on `df1eb5a15`, UNPUSHED

The branch now carries THREE subjects, which is worth knowing before you review
it: the CLI command registry (below), the CI cache-signing fix, and the agents
guide split merged in from `feat/frosty-diffie-660151`. Splitting them is much
cheaper before the push than after.

The original ask was "fix the MGS1020 failure". That is fixed in the first
commit. Everything after it came out of one thesis: the CLI's flags were
declared twice - once where they were bound, once in a hand-written copy for the
man pages - and a test reconciled the two by NAME only. Every drift below was
already shipping when this started.

## State

Feature complete for the registry work. `magus affected ci --no-default-charms`
is GREEN across every project as of `2f5d9a28e`, tree clean afterward, so
`generate` found no drift as a pure gate.

CI is NOT verified past `f6e3cfc7d` - nothing since has been pushed. The last CI
run (`fab95557a`, run 31815786600) failed in `magus plan` on four drifted
`libs/*/MAGUS.md`; `b5695d3d6` commits exactly those four, so that specific
failure is fixed, but only a push proves it.

**`magus report` has not run on this branch in a long time.** The job is gated
on `needs.plan.result == 'success'`, and `plan` has been red, so it SKIPS rather
than fails - which is why the "magus report is red" item further down was stale.
When `plan` goes green the report job executes for the first time in a while.

## The CI cache was replaying UNSIGNED artifacts

Not part of the registry work; found while chasing the stale report item, fixed
in `9cb13ae1c`.

`ci.yaml` set `MAGUS_CACHE_REMOTE_INSECURE: 'true'` workflow-wide. Two things
were wrong at once:

- The magusfile wires the remote cache on `trusted_keys OR insecure`, and
  `vars.MAGUS_CACHE_PUBLIC_KEY` is unset, so the trust set was empty and the
  INSECURE FLAG WAS THE ONLY THING TURNING THE CACHE ON.
- `remoteCacheSigningOpts` (magus.go) returns `WithInsecureRemote()` BEFORE it
  reads a trust set, so the `trusted_keys` in `magus.yaml` were never consulted.

So the workspace shipped a trust anchor that verified nothing. The flag is gone
from `ci.yaml`; with the variable unset the cache is simply not wired, which is
no error and no remote hits rather than a break.

`gh secret list` shows no `MAGUS_CACHE_SIGNING_KEY`, so the public key in
`magus.yaml` is ORPHANED - no seed exists to sign with. Turning verification on
for real needs a fresh keypair, which needs a human: the runbook is in
`docs/concepts/cache/remote.md` ("Runbook: turning it on for a GitHub
repository").

`1c7afa78f` makes that runbook possible without the seed touching disk:
`magus config cache key generate -o template='{{.seed}}'` puts the seed alone on
stdout (warnings to stderr, no trailing newline, since `gh secret set` stores
stdin verbatim), and `--tee` is REFUSED on that subcommand because it writes
structured output to a file.

Note the template field names are the `-o json` tags: `{{.seed}}`, not
`{{.Seed}}`. The Go-field spelling fails at runtime, and it is an easy thing to
write into a doc without executing.

## What shipped

**Every command binds its flags from `internal/clispec`.** The package holds the
CLI's declarative surface; `cmd/magus/gen/cli_flags.go` is generated from it
(`magus-utils cliflags`) and carries a name constant per flag plus a typed
binder per command. There is no second list to keep in sync.

The registry grew five concepts. Each exists because something concrete broke
without it, not because it seemed general:

| concept | added because |
| --- | --- |
| `Flags []Flag` (data, not a closure) | a closure can only be replayed into a FlagSet, so nothing could read the names at generate time |
| `AliasOf` | `-t` and `--test` got two struct fields, so the shorthand parsed and did nothing |
| `DefaultAtBind` | `--max-shards` froze `8` into the binder where the CLI reads `ci.max_shards` |
| `FlagCustom` | repeatable `flag.Value` flags, and mode selectors read from argv before any FlagSet exists |
| `Modes` | `affected` has four parses; one merged list would accept `--max-shards` on a plain run and ignore it |

Also: `internal/manpage` was renamed `internal/clispec` (six consumers, only one
of them a man page), the browser terminal now reads the same registry for
completion and help, and `magus agent install --global` works for the first time.

**The run summary left `.github/actions/magus`.** That action is "invoke a magus
subcommand, or merge per-shard run histories"; its `report` mode invoked neither -
it ran a Buzz script that reads the workspace through the typed `magus\insight`
client. It is now `.github/actions/ci-outcome`, beside `.github/actions/advice`,
and the `report` and `shard-count` inputs are gone (`ci-result` stays; merge mode
records it). ci.yaml's `report` JOB keeps its name and now composes the two
actions. The `always()` that the composite's merge steps carried moved up to the
workflow step, or a red summary would silently skip the history merge - the case
history matters most in. `advice-test` sweeps the new directory too.

## Bugs fixed that predate this work

- `--shard` was documented `int` and bound `string`. The name-only drift test
  could never see a type disagreement.
- `agent install --global` never worked: the CLI permitted an absolute
  destination and `Catalog.WriteSkillTree` refused it unconditionally, so the
  error told you to pass the flag you had just passed.
- `query --url` was bound to `defaultLogViewerURL` and declared with no default,
  so the man page documented none.
- 25 flags were bound but declared nowhere, so no man page listed them. Among
  them `server stop --socket`, `server stop --services`, `describe spells
  --versions`, `watch --ignore`, `memory put --ref`, and all 14 under `config`.
- `describe -e` meant `--explain` under one noun and `--evaluated` under
  another. Splitting the nouns into real commands dissolved it.

## The agents guide split, merged in

`2f5d9a28e` merges `feat/frosty-diffie-660151` (3 commits, based on the same
`df1eb5a15`). That branch is still checked out in the
`agent-guard-harness-evolution-92bc5f` worktree and is now fully contained here,
so it is safe to delete.

The merge driver settled all 20 generated files by keeping the current version;
regenerating afterwards needs TWO passes, because `generate` rewrites docs and
the knowledge graph then indexes those docs. Four source conflicts, all resolved
toward frosty.

The one that mattered: `agents.md` had edits on BOTH sides, and taking frosty's
whole file could have dropped this branch's. It did not, because both branches
had independently corrected the same thing - the removed `--agents-md` flag -
and frosty's `227a5655a` is exactly that fix. Verified before dropping the
monolith: `--agents-md` appears nowhere in frosty's tree, and `cursor.md` plus
`skills.md` carry the "magus never writes it, you paste the block" wording.

Backup tags `backup/pre-frosty-improve-terminal` and `backup/pre-frosty-frosty`
still exist; delete them once the merge is trusted.

## Open: needs a decision

**1. STALE - see State above.** ci.yaml's `report` job is not red, it is
skipped, because `plan` gates it. Kept because the root-cause analysis below was
also wrong and is worth not repeating: the claim that
`MAGUS_CACHE_REMOTE_INSECURE` CAUSES the "no trust set is declared" error does
not reproduce, and cannot - the insecure branch returns before that error is
reachable. The real defect was the opposite one, above: the flag suppressed
verification entirely. The repro one-liner below no longer compiles (BZZ1006),
which is a fair sign it had not been re-run.

Not caused by this work - `main` at `df1eb5a15` fails identically. Root cause is
pinned. Reproduce:

```bash
MAGUS_CACHE_REMOTE_INSECURE=true magus buzz -e 'import "magus"; fun main() > void !> any { magus\ls(); } main();'
```

That variable makes the workspace fail to open with "a remote cache backend is
wired but no trust set is declared" - the exact condition the flag documents
itself as waiving. `magus config view` resolves it correctly (`insecure=true`,
trusted non-empty), and `magus ls` is unaffected; only the `buzz` path breaks,
which is the one using `WithLoadedConfig(globalCfg)`. `ci.yaml` sets that
variable workflow-wide, so `.github/actions/ci-outcome/ci-outcome.buzz` cannot open
a workspace and `magus\insight` raises MGS1022.

Two fixes, and the second is a security-posture change that is yours to make:

- Fix the layering so the env var stops clobbering `cache.remote` from
  magus.yaml. Principled; fixes main too.
- Drop `MAGUS_CACHE_REMOTE_INSECURE` from `ci.yaml`. One line, but CI would then
  verify signatures instead of skipping them.

## Open: pre-existing defects found while reviewing, not fixed

None of these are regressions. They were found by the review pass over the
changed surface and left alone because they are outside this PR's subject.

1. `-o jsonl` emits a top-level slice as ONE array line. `writeJSONL` handles
   `reflect.Struct` and falls through for `Slice`. `describe target --cache`
   builds `[]targetCacheReport` and hits it. This is the format agents script
   against, and it is wrong today.
2. The invocation journal records **pass** on a failed run. `run.go`'s
   `defer func() { endInvocation(err) }()` captures `err`, but the terminal paths
   `return runChain(...)` and `return emitRunResult(...)` never assign it.
3. `--tee`'s `Close` error is discarded (`_ = cleanup()`), so a full disk exits 0
   with truncated output.
4. `ResolveOutput`'s unknown-format error lists only `CommonFormats`, omitting
   the extras the call site just passed: `graph export -o graphmll` is told to
   choose from a list that excludes graphml, dot and mermaid.
5. `writeJSONL`'s "exactly one slice field" heuristic silently changes output
   shape when a second slice field is added. `runOutput` already has two.

## Open: asymmetries in what this PR added

1. `defaultOf` (clispec/spec.go) silently substitutes the zero value when
   `Flag.Default` has the wrong type, four lines from a `Kind` switch that
   PANICS with reasoning that applies identically. That was defensible while the
   registry only fed documentation; the binders are now the real CLI, so a wrong
   `Default` is a runtime behaviour bug. `flagDefaultLiteral` in the generator
   repeats the same silent fallback.
2. `writeBinder` does not dedup flag names within one command; `constsFor` does,
   but only for the const block. A duplicate name inside one `Flags` list emits
   two identical struct fields and two `fs.XVar` calls, which fails to compile
   (and would panic "flag redefined" if it did not).
3. `FlagCustom` flags are declared but bound by hand. Nothing checks that the
   hand binding still exists, so deleting one would leave the flag documented
   and gone.

## Traps worth knowing (cost real time here)

1. **`magus run go::go-build .` is NOT a compile check.** It replays cached
   passes and reported success twice on code that did not build. Only
   `magus run go_build . --no-cache` is trustworthy. Worth a line in CLAUDE.md.
2. **`man-generate` declares no `readsFiles`**, so its cache key misses
   `cmd/magus/**` and it replays stale manpages. A regenerate that "succeeds"
   can leave the previous binary's output in place; use `--no-cache` when the
   CLI surface changed.
3. **`hostagnostic_test.go` skips directories by BASE NAME.** Its list contains
   `manpage`, which was meant for the generated `manpage/` output at the repo
   root and silently also skipped `internal/manpage/` for as long as that
   package existed. Renaming it to `clispec` exposed a real violation that had
   been hiding. Any package whose directory name collides with that list escapes
   the gate.
4. Converting a command is only safe if you DELETE the old locals in the same
   edit. Swapping the binding alone compiles, and every flag then silently reads
   zero. Deleting the locals turns that into a compile error listing every read
   site.
5. `magus run lint` needs node 24 on PATH or it fails MGS3006 on the console and
   docs projects: prefix with
   `PATH="$HOME/.local/share/mise/installs/node/24.19.0/bin:$PATH"`.

## Verification commands

```bash
magus run go_build . --no-cache      # the only real compile check
magus run test . --no-cache
magus run lint . --no-cache          # includes the wasm compile gate
magus run generate .                 # then commit any regenerated output
```

`TestManpageFlagsMatchTheCLI` is deliberately retained. It no longer reconciles
two hand-written lists, but it still verifies the BOUND set against the DECLARED
set through the real `-h` path, which is a runtime check a source comparison
cannot make.
