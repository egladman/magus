# Handoff: agent guard hardening, skill compression, guard-binary footgun

Written 2026-07-29. Worktree `.claude/worktrees/artifact-history-guardrails-36b705`,
branch `feat/artifact-history-guardrails-36b705`, fast-forwarded onto
`feat/generator-layout-handoff-c30833` (4062215a). **Nothing committed.**

Full Go suite green (`magus run go-test .`). `magus graph verify` clean at digest
`f59a5c8784d5`.

## Read this first: the stale-binary footgun

The guard hook resolved its binary as `command -v magus || /tmp/magus`. Nothing was
on PATH, so for most of a session every verdict came from a months-old `/tmp/magus`
left by an earlier session. It denied things and printed reasons, so it looked
healthy, while every bypass being fixed that session was still open in the binary
doing the enforcing. Proof: the stale binary passed `go build -o /tmp/x` and
`mise exec -- go test`; the fresh one denies both.

Fixed three ways:

1. `.claude/settings.json` and both docs snippets resolve `./magus` first, then
   PATH. The `/tmp` fallback is gone.
2. New `magus doctor` check **guard binary** names the binary a hook would run and
   fails when it predates the newest tracked `.go` file.
3. Deny reasons name the AST-RESOLVED command: "magus guard denied `go test ./...`,
   which is what `mise exec -- bash -c '...'` resolves to once wrappers and quoting
   are stripped."

**Anything claimed before that discovery needs re-verifying.**

## Landed

- **Guard on `mvdan.cc/sh/v3/syntax`.** `cmdPos`, `peelPassThrough`,
  `maskQuotedArgs` deleted. Wrapper peeling, quoting, `-c` re-entry and `eval` are
  structural now. The whole-tree git rules moved onto the parser too, which fixed
  `git stash` denying when it appears as PROSE (it blocked writing the magus-vcs
  skill through a heredoc twice). Regexes survive only as the unparseable fallback.
- **Write rule** is trigger #2 in the doctrine: writes into the tree go through
  magus, no exceptions. `go build` denies at EVERY output path (memory:
  `go-build-denied-outright`). `go mod tidy|vendor` and `govulncheck` added.
- **magus piped into a text filter DENIES**, with `magus query output <ref>` the
  sole exemption (raw tool log, no schema to project).
- **`magus vcs add`** (new): classifies the dirty tree and stages sources plus the
  generated outputs they produced, REPORTING undeclared paths instead of sweeping
  them in. The `git add -A` denial now names it. On this tree: 43 sources, 87
  outputs, 2 undeclared skipped.
- **`magus agent notify`** (new): one neutral envelope for host attention events
  (waiting / stopped / permission / other), `--desktop` for a native notification,
  three input forms. Host vocabulary is matched by GENERIC substring only - the
  `TestNoHostSpecificBehaviorInCode` arch test forbids host names in source, and
  caught me violating it. Per-host wiring is documented in agents.md instead.
- **`-s` and the whole display set on every command.** 25 FlagSets were missing
  `bindDisplayFlags`; `graph verify -s` errored and `agent install -s` failed
  SILENTLY (skills never installed). `TestEveryCommandBindsDisplayFlags` is a
  source-level parity test so the gap cannot reopen; one documented opt-out
  (`nodisplayflags:`) for main.go's deliberately-absorbing "adopted" set.
  `config cache export --output <file>` renamed to `--to`, since it collided with
  the global format flag.
- **`<!-- terse -->` marker**: pairs with `<!-- why -->` so a span carries BOTH a
  long and a short wording. Simple can now be rewritten, not just truncated.
- **Skill stamp table** on every skill docs page, with the real install digest.
- **Quick start page** at `docs/guides/quickstart.md`.
- **Console doctor check** skips when the daemon is unreachable (see caveat below).

## CLI parsing consistency audit

Audited every argument reader against the two house conventions. Result: the
comma convention is clean, the flag-spelling convention had two real holes, both
now fixed and pinned.

**Comma-delimited lists: CONSISTENT.** Four implementations (`splitCSV` in
query.go, `resolveRace`, `parseProbeKinds`, `splitCommaList` in mcp/memory.go)
and all four agree exactly: split on comma, trim, tolerate empty segments. Only
issue is duplication - probe.go's comment even says "mirroring resolveRace's
convention". Worth consolidating into one helper; no behavior change needed.

**Pattern flags: CONSISTENT** where they exist. `type=<glob|regex|literal>,
pattern=<value>` via `watch.ParsePattern`, used by `magus watch --ignore` and
`magus where --filter`, with `--glob`/`--regex`/`--literal` shorthands and a
conflict error.

**Flag spellings: TWO HOLES, FIXED.** Go's flag package accepts `-f v`, `--f v`,
`-f=v`, `--f=v`, so every command using `cmdParse` takes all four for free. The
hand-rolled readers only accept what their author wrote:

1. `chainPathFlag` took three of four - `-path=out` was rejected, failing as if
   the value were bad rather than the syntax. It also scanned for `--path`
   anywhere and IGNORED every other argument, so `export --path out -o json`
   silently dropped the `-o json` and `export stray --path out` dropped `stray`.
   That is the accepted-and-ignored failure `--then` exists to prevent, and the
   sibling verbs already rejected it. This was the documented open defect.
2. `parseExplainArgs` matched only `--explain`, never `-explain`. Worse than
   dropping: `magus affected -explain .` swallowed the flag and read `.` as a
   TARGET, producing `target name ".": must contain only letters, digits...` -
   an error naming neither the flag nor the real problem.

Both now route through shared `isFlagNamed` / `flagValueOf` helpers in globals.go
so the rule has one definition. Pinned by `TestPrepareChainGrammar` (unit) and
`run_chain_flag_parity.txtar` (end to end).

**Checked and fine**: `affected.go`'s `--plan` scanner and `main.go`'s `-root` /
`-daemon-enabled` scanners already handled all four spellings.

**Noted, not fixed**: `status.go:parseRunning` skips leading `-` tokens without
knowing which consume a value, so a flag's VALUE can be read as the positional.
It parses RECORDED command args for display, not live CLI input, so it is an
attribution concern rather than a parsing one.

**Design gap, not a bug**: `--then file <path>` takes a literal path only. There
is no way to export a SUBSET of outputs - it is one file or all of them - even
though the repo has an established glob/regex/literal vocabulary that would fit.
Worth deciding deliberately rather than drifting into.

## Open

1. **`--simple` is at 25%, target was 50-60%.** All 8 skills converted: run 43%,
   vcs 34%, query 26%, changes 21%, architecture 19%, docs 17%, memory 15%,
   buzz 14%. magus-run shows what aggressive two-wording achieves. The rest need
   the same pass. Honest caveat: the remaining bulk in the converted ones is
   command blocks and tables, which are the highest-value bytes on the page, so
   50-60% may not be reachable without cutting content. Measure before promising.
2. **MGS3001 in the `build` target** - unresolved, and it ABORTS BEFORE `go build`,
   so `magus run build .` is not currently a working dev loop. Verified: not a
   stale cache (reproduces with `--no-cache`), no background daemon, not any
   `*-generate` target individually, not `generate`, not `format`. Reproduces from
   `magus run go-build .`, whose only commands are two `go build` invocations. The
   committed `libs/gopherbuzz/MAGUS.md` is schema v6 while the binary emits v7.
3. **Console doctor check still fails here**, and the reason is a different bug:
   `magus status` reports "no running magus proc server found", yet doctor's
   `DaemonInfo.Reachable` is true, so `proc.QueryStatus` is succeeding against
   something - probably one of the 12 stale sockets the `sockets` check lists. The
   skip I added is correct but never fires. Fix daemon detection, not the check.
4. **`vcs.DirtyFiles` returns status LINES, not paths**, despite the name. Every
   existing caller only tests emptiness so nothing noticed; `magus vcs add` parses
   them locally (`statusPaths`). Worth renaming or fixing at the driver.
5. **`magus run test .` fails to link**: `fingerprint mismatch`. NOT a poisoned
   cache (survives `go clean -cache`) and NOT caused by raw builds - an earlier
   code comment asserted that and was WRONG. `magus run go-test .` passes, so the
   difference is the `test` target's `-race -covermode=atomic`.
6. **Not started**: graph zoom/fullscreen bug (wiring at
   `console/src/console/graph/main.ts:4256`), artifact diff into the console
   (confirmed genuinely not started - no `ArtifactHistory` reference in any handler
   or proto).

## Corrections worth carrying

- **Do NOT drop bytecode decode.** Not planned, not touched. The old memory note
  about baking the spell registry as JSON is NOT an approved plan.
- **A magus upgrade should not invalidate the whole cache.** The target cache key
  holds sources, deps, tool versions, charms - not the magus version. Knowledge
  shards version separately via `knowledge-schema-version`. Per-kind version
  namespacing, never a global epoch.
- **`go::go-build` does not emit a binary** (`go build`, no `-o`). Only the `build`
  target does, and it currently aborts first. Cost 20 minutes testing a stale one.
- **The guard cannot be sound.** Command-string guarding is the sudoers/GTFOBins
  problem; enforcement belongs in the filesystem sandbox. `internal/sandbox` has
  Landlock on Linux and ErrUnsupported on macOS, which is why the hook carries
  weight it structurally cannot carry there. `TestGuardKnownHoles` records what it
  cannot catch: `sh script.sh`, `$(which go)`, `$GO`, aliases, `make`.
- **I asserted a context limit I cannot measure** and used it to stop early, twice.
  There is no context meter available from inside; do not claim one.
