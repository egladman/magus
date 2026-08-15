# Diff surface: handoff

## READ THIS FIRST - where the work is

**Branch: `feat/pwa-diff-viewer-replay-afea09`**
**Worktree: `/Users/eli.gladman/Repos/magus/.claude/worktrees/wonderful-maxwell-80473c`**

**Continue ON THAT BRANCH, IN THAT WORKTREE. Commit there. Do not start a new branch, do not
open a new worktree, and do not re-derive any of this somewhere else.** Forty-seven commits of
work live there and nowhere else - it is UNPUSHED and has no upstream, so nothing is
recoverable from a remote if it ends up stranded on a branch nobody remembers to merge.

```sh
cd /Users/eli.gladman/Repos/magus/.claude/worktrees/wonderful-maxwell-80473c
git status --porcelain   # expect clean
git log --oneline -5
```

If that worktree is gone, recreate it against the SAME branch rather than branching fresh:

```sh
git worktree add /Users/eli.gladman/Repos/magus/.claude/worktrees/wonderful-maxwell-80473c feat/pwa-diff-viewer-replay-afea09
```

Two other branches are RELATED but are not where work goes:

- `feat/diff-viewer-market-research-b03191` - the research that drove all of this
  (`plans/diff-viewer-market-research.md` plus `plans/diff-viewer-personas/`, seven persona
  reports). READ-ONLY reference. Do not implement there.
- `feat/beep-129522` - **already folded in** (merge `f916d25ee`). Its worktree
  `agent-guard-harness-evolution-92bc5f` is now clean. Do NOT merge it again.

Tree state at handoff: clean, root suite green, console lint and tests green, zero lint findings
in this tree.

## What this surface is

`magus diff` - the working tree's changes, annotated from the build graph and ordered by what
they can break, with declared target outputs folded away. Three clients over one daemon-held
session: the CLI, the console's Diff surface, and the `magus_diff` MCP tool.

## The rename, in case something still says "review"

`magus review` -> `magus diff`, and with it `magus_review` -> `magus_diff`, `types.Review*` ->
`types.Diff*`, `internal/review` -> `internal/diff`, `magus\review` -> `magus\diff` (Buzz),
`SortForReview` -> `SortForReading`, and the console command ids `review.*` -> `diff.*`.

**The endpoint split is the part to remember.** `/api/v1/diff` was already the raw patch, so:

| Route | Serves |
| --- | --- |
| `GET /api/v1/diff` | the ANNOTATED changeset (was `/api/v1/review`) |
| `GET /api/v1/diff/patch` | the raw unified patch (was `/api/v1/diff`) |
| `POST /api/v1/diff/session` | the session, unchanged in meaning |

Handlers follow that vocabulary: `DiffHandler` is annotated, `PatchHandler` is text. The rule is
**Patch is the text, Diff is the annotated object** and it is worth keeping. The word "review"
survives where it means the ACTIVITY, which is correct.

## What landed

Grouped by what it fixes. Every one is committed on the branch above.

### Honesty of the ranking

- **The UNRANKED banner.** `magus diff` claimed "ordered by what they can break" while
  rendering path order whenever the symbol index was cold. `Reach` is now `*int` (nil is
  unmeasured, distinct from a measured zero), `Diff.Ranked()` reports whether a ranking key
  existed at all, and both the CLI and the console say UNRANKED before the list.
- **A positional is refused.** `magus diff main` used to print your own uncommitted edits under
  exit 0. Now only `-` or a readable patch file is accepted.
- **`--help` no longer overclaims.** It named reach, surface, and coverage as rank inputs; only
  reach ranks. It now states the sort keys and calls the rest context.
- **Seeds are author-edited.** A project whose only change is a folded generated file is no
  longer counted as "edited"; `AffectedProjects` still keeps the full closure.
- **A file with no history reads sooner, not last.** `NoHistory` - absence of evidence was
  sorting a brand-new file to the bottom, which is where the one real bug was in a persona run.

### The agent half (`e4952da9a`)

- **The session was stale by construction.** `Attach` had one production caller - the console's
  HTTP handler - so an agent joined a changeset frozen at whenever a browser last fetched. The
  session now carries `AsOf` (the patch digest) and `op=state` RECOMPUTES when the tree moved,
  reporting `recomputed: true`.
- **`op=state` now ships `patch` and `hunks`** (0-based index plus content digest per hunk), so
  a comment can cite a coordinate instead of guessing one, and `viewed` is joinable at last.
  `comment` and `suggest` REFUSE a path not in the change and a hunk index that does not exist.
- `agent_name` is recorded on a comment (it was silently dropped), and SCIP locals are dropped
  from the payload - they were about two thirds of it.

### The console

- **Colour.** The add/delete rules named `--pf-t--global--background--color--status--*` tokens
  PatternFly **does not define**, so they computed to transparent and tinting had NEVER
  rendered. Now mixed from the status COLOUR tokens that do exist, with an inset accent bar
  (not a border - a border grows the content box and drifts the fixed row height). Sidebar and
  toolbar moved onto the secondary surface so the panels stop reading as one sheet.
- **Syntax highlighting** (`console/src/console/diff/syntax.ts`), which the surface never had.
  A small bundled tokenizer: a strict CSP means bundled or absent. Ambiguity stays plain, but
  unclosed block comments and leading-`*` continuations are read, because that is what this
  codebase looks like on screen.
- **Word-level emphasis** (`words.ts`) - which PART of a changed line changed.
- **Accessibility.** `aria-rowcount` is the true total, `aria-rowindex` the ABSOLUTE position,
  add/delete announced in words with the glyph `aria-hidden`, a polite live region on the
  suggestion rail, and `prefers-reduced-motion` honoured in CSS and in `scrollTo`.
- **`/console/<surface>` redirects to `/console/<surface>/`.** The shell is served with
  `<base href="../">`, which only resolves when the URL ends in a slash - so a bare
  `/console/diff` 404'd every asset and never booted.

### Terminal and elsewhere

- `magus diff -` and `magus diff <patch-file>`; `--watch`; OSC 8 hyperlinks on every path.
- `Touch.Ran` stores the PROGRAM, never the argument list. **A live daemon bearer token was
  observed leaking through it.** See "Yours to do".
- Doctor `agent observer` check: fails on commands-with-no-reads, advises when reads are too
  sparse, quiet below 50 observations.
- **Sapling backend plus a cross-backend VCS parity suite**, folded in from `feat/beep-129522`.

## Yours to do (not mine)

**Rotate the daemon token.** Still the one recorded in memory as leaked into published docs - I
confirmed the same value is live.

```sh
rm -f "${XDG_STATE_HOME:-$HOME/.local/state}/magus/mcp_token" && magus server stop && magus server start
```

## NOT DONE

Not started rather than half-built.

- **"What changed since you last looked."** `AsOf` plus the persisted viewed digests now hold
  everything this needs.
- **A projection parameter on the MCP ops.** Every op returns the whole session, and shipping
  `patch` made that bigger, so this matters more than it did.
- **Gate mode** (exit code / threshold). A persona shipped a changeset whose tests did not
  compile; this is a prioritizer, not a gate.
- **Knowledge-loss / bus-factor join.** The ownership lens exists and the diff never reads it.
- **A diagnostic when a target writes a path it did not declare** - what would make the fold's
  completeness checkable rather than promised.
- **Extract one shared syntax palette.** The console's `--console-syn-*` values are COPIED from
  the docs site's `--syn-*` (One Light / One Dark). Two lists that must be edited together.
- **Buzz in WASM.** Scoped, not built. `cmd/buzz-playground` already compiles the interpreter to
  WASM for the DOCS site; `internal/dry` mirrors the `magus` surface for it and
  `TestMagusSurfaceMatchesBindings` keeps them in step. `types.Diff` is already a Buzz runtime
  object. **The blocker is CSP:** `internal/service/console/static.go` pins `script-src 'self'`
  and WASM needs `'wasm-unsafe-eval'` added - which permits compiling WASM and still forbids JS
  `eval`. Note highlight.js 11.11.1 and CodeMirror 6 are `docs/package.json` deps, and that file
  states the boundary outright: the console owns its own bundles.

## NEXT: the TUI

The agreed next piece is a text UI at parity with the console, CLI-first.

**Its reason to exist is not visual.** The CLI is not actually a client of the session:
`cmd/magus/diff.go` shares the review COMPUTATION and none of the COORDINATION - no comments,
no cursor, no viewed marks. So "three clients, one session" is really two, and the denial
architecture ("the agent suggests, you navigate") only means anything in the browser. A TUI is
what makes that true for terminal users.

- **Reuse, do not reimplement:** `SortForReading` is authoritative (the console consumes it and
  says so in `order.ts`), plus `ParseHunks`, `HunkDigest`, and the UNRANKED rule - all Go-side.
- **Move `words.ts` to Go** so intra-line emphasis serves CLI, TUI, and console from one place.
- **Parity list:** hunk stream with `[`/`]`, split/unified, generated fold toggle,
  Escape-to-overview, viewed marks by content digest, comments inline, suggestion rail, and the
  same refusal - an agent never moves your cursor.
- `internal/interactive/{tty,screen}` is the foundation.

**One open design question, unanswered:** explicit `magus diff --tui`, or interactive
automatically when stdout is a TTY and no `-o` format was requested? The second is more "CLI
reads context, never asks", but it changes the default behaviour of a command that currently
prints a scrollable report - and that report is what the unix-graybeard persona liked.

## Traps worth knowing

- **`graph != nil` is NOT `HasSymbols()`.** A knowledge graph loads fine with no symbol shards,
  so gating on non-nil reported unmeasured reach as a measured zero and the UNRANKED banner
  never fired. Compile and full suite were both green; only running the binary found it.
- **Console digests must match TypeScript byte for byte.** `internal/diff.HunkDigest` is pinned
  against a node-computed literal in `TestHunkDigestMatchesTheConsoleImplementation`. If they
  diverge, a hunk marked read in the browser goes invisible to the CLI and to an agent -
  silently, and only for some hunks.
- **`magus run lint .` leaks findings from sibling worktrees.** 25 come from
  `../improve-terminal-insight` (go1.27 `stdversion`) and have nothing to do with this branch.
  Filter to paths that do not start with `../`.
- **Rebuild before trusting a daemon check.** A stale `./magus` had me conclude `as_of` was
  broken when it simply was not in that build. And stop the daemon before `go_build` - it holds
  the binary.

## Not verified

- The console was exercised in a real browser (grid semantics, absolute `aria-rowindex` under
  scroll at 122 rows, word emphasis, tint, redirect) - but there is **no DOM test** for any of
  it; the cover is unit-level plus that manual pass.
- `--watch` compiles and is wired; nobody has sat in a watch loop and edited a file.
- The Sapling backend's tests run only where `sl` is installed. It is installed on this machine,
  so they genuinely ran rather than skipped.
