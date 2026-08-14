# Handoff: the CLI command registry

Written 2026-08-14. Everything below is on ONE branch and ONE worktree; keep
working there and keep pushing there.

- Branch: `improve-terminal` ([PR #52](https://github.com/egladman/magus/pull/52))
- Worktree: `.claude/worktrees/improve-terminal-insight`
- 55 commits ahead of `origin/main`, rebased onto `df1eb5a15`

The original ask was "fix the MGS1020 failure". That is fixed in the first
commit. Everything after it came out of one thesis: the CLI's flags were
declared twice - once where they were bound, once in a hand-written copy for the
man pages - and a test reconciled the two by NAME only. Every drift below was
already shipping when this started.

## State

Feature complete for the registry work. Local `magus run test .` and `magus run
lint .` are both GREEN uncached, and lint includes the `GOOS=js GOARCH=wasm`
compile gate.

CI is NOT verified past `f6e3cfc7d`. The last several commits have only been
tested locally. Watch the run before merging.

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

## Open: needs a decision

**1. `magus report` is red, on this branch and on `main`.**

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
variable workflow-wide, so `.github/actions/magus/ci-outcome.buzz` cannot open a
workspace and `magus\insight` raises MGS1022.

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
