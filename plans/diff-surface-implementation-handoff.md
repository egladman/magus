# Diff surface: handoff

## READ THIS FIRST - where the work is

**Branch: `feat/pwa-diff-viewer-replay-afea09`**
**Worktree: `/Users/eli.gladman/Repos/magus/.claude/worktrees/wonderful-maxwell-80473c`**

**Continue ON THAT BRANCH, IN THAT WORKTREE. Commit there.** Fifty-plus commits of
work live there and nowhere else - UNPUSHED, no upstream. If the worktree is gone,
recreate it against the SAME branch:

```sh
git worktree add /Users/eli.gladman/Repos/magus/.claude/worktrees/wonderful-maxwell-80473c feat/pwa-diff-viewer-replay-afea09
```

Related, read-only: `feat/diff-viewer-market-research-b03191` (the research).
`feat/beep-129522` is already merged - do not merge it again.

Tree state at this handoff: clean, `magus affected ci --no-default-charms` GREEN
(2026-08-15, daemon stopped so nothing forwarded), console suite green
(296+25+8, tsc clean).

## What this surface is

`magus diff` - the working tree's changes, annotated from the build graph and
ordered by what they can break, with declared target outputs folded away. Now
FOUR clients over one daemon-held session: the CLI report, the TUI, the
console's Diff surface, and the `magus_diff` MCP tool. Patch is the text, Diff
is the annotated object.

## What landed 2026-08-15 (this session)

- **`magus diff --tui`** - the terminal joined the session. Inline,
  scrollback-preserving viewport (the alt-screen ban in tty/ansi_test.go is
  honored, not fought). Keys mirror the console: `]`/`[` hunk, `}`/`{` file,
  `v` viewed, `.` generated fold, Escape overview, PageUp/PageDown (newly
  decoded in tty/input.go), q/Ctrl-C quit leaving the counts line in
  scrollback. Attaches via GET /api/v1/diff with the bearer token when the
  daemon is reachable, sends {op:cursor}/{op:viewed}, REFUSES a session whose
  AsOf digest is not the local patch (a daemon serving another checkout cannot
  mislabel your hunks); degrades to in-process compute + the local viewed
  store when no daemon. Refusals (exit 2): patch/stdin source, --watch, -o
  formats, no TTY. The navigation model is a plain struct, headless-tested in
  internal/interactive/difftui.
- **Word-level emphasis in Go** - internal/diff/words.go ports words.ts
  (rune-scan, byte offsets, ASCII WORDISH preserved); 19 parity vectors
  COMPUTED BY RUNNING the real TS under node 24, provenance in the test.
  Consumers not yet wired - the TUI renders without emphasis today.
- **`magus_diff` projection parameter** - op=state accepts
  projection=full|summary|conversation|patch (default byte-pinned to the old
  shape by TestProjectionFullMatchesTheOriginalStateResponse). Writing ops
  ignore it, documented and pinned.
- **Hunk payload tagged for the wire** - FileHunks/Hunk carry json tags now;
  agents see path/index/header/lines/digest, not Go-cased keys. Caught by
  musttag once a test marshaled diffState directly.
- **Stale review-era text fixed** - the MCP no-session error no longer
  recommends the nonexistent `magus review`; std/magus.go and
  mcp/options.go comments name the real command and routes; order.ts and
  session.test.ts name SortForReading and the real test path.
- **Console syntax palette pinned** - syntax-palette.test.ts asserts the 5
  light + 5 dark `--console-syn-*` values equal the docs site's `--syn-*`
  (fault-injection verified). Values were already equal; now drift is a build
  failure. Option A (generation) was rejected: no generator seam crosses the
  console-owns-its-bundles boundary.
- **Daemon token ROTATED** - the leaked mcp_token (Jul 10) is deleted; the
  next `magus server start` mints a fresh one. Both memory entries about
  rotation are settled by this.

## NOT DONE (unchanged unless noted)

- "What changed since you last looked" - AsOf + viewed digests still hold
  everything needed; no UI consumes it yet.
- Gate mode (exit code / threshold) - still undesigned; decide what failing
  MEANS first.
- Knowledge-loss / bus-factor join.
- Diagnostic when a target writes an undeclared path - now cross-linked to
  the delegation surface's describe-file facts work (same honesty thesis).
- Buzz in WASM (CSP: script-src needs 'wasm-unsafe-eval').
- TUI v1 gaps, deliberate: no word-emphasis wiring, no color on +/- lines
  (biggest cheap legibility win), agent-trail Touches not rendered, mouse
  wheel dead while open (OpenInput enables tracking; wheel-to-Scroll is ~4
  lines), comment compose/resolve and suggestion accept/decline not built
  (display only), split view not built. Cursor sync is a synchronous POST per
  navigation key (1s timeout worst case against a wedged daemon).

## Traps (new ones from this session first)

- **A daemon-forwarded `magus run` can swallow output AND exit status.**
  Observed: EXIT=1 with zero output, then EXIT=0, equally silent; -vvv showed
  only startup.daemon_forward. Workaround: MAGUS_DAEMON_ENABLED=false, or
  stop the daemon before gates. A green forwarded run is not evidence.
- **This worktree's guard denies ALL raw go commands** (test included - the
  merged guard-evolution branch is stricter than main). Validation goes
  through `./magus run go::go-test . -- -run <pattern>`, which still compiles
  the whole module; narrow package runs are impossible. Consequence, learned
  the hard way: two agents with disjoint WRITE sets still collide on
  VALIDATION (whole-module runs sweep another agent's mid-edit files).
  Serialize validation in a shared worktree even when edits are disjoint.
- **A lint finding with a `../` prefix is the sibling-worktree leak; one
  WITHOUT the prefix is yours.** A musttag finding was misdismissed as leak
  this session because an older log showed it under ../improve-terminal-insight.
  Check the path prefix in the CURRENT gate log before dismissing anything.
- `graph != nil` is NOT HasSymbols(); console digests must match TS
  byte-for-byte; rebuild before trusting a daemon check (all still true).

## Not verified

- The TUI has never been driven on a real pty - the input loop, InlineView
  painting, and OSC 8 rendering are covered by headless model tests only.
  Run `./magus diff --tui` in a real terminal before trusting the feel.
- --watch still has nobody sitting in the loop.
- The console changes since the last manual pass are test-only; the browser
  was not reopened.

## NEXT candidates, in rough order

0. Advisors run LOCALLY in magus diff (Eli's direction, recon complete): a
   local driver mode for the .github/actions/advice .buzz advisors -
   base/head from the working tree, collect-dont-publish - rendering advisor
   findings as a diff-report/TUI/console section. The "open in console" line
   is the thin edge of this.
1. Wire words.go into the TUI and CLI (the port exists, consumers do not).
2. Color in the TUI, then wheel scrolling - the two cheap legibility wins.
3. Comment compose in the TUI (the composer precedent is the picker's filter
   typing).
4. "Since you last looked" as a projection/filter over viewed digests.
5. Session persistence to disk (daemon restart currently drops comments,
   suggestions, cursor; viewed already survives) - also what stateless MCP
   wants; see plans/stateless-mcp-design-note.md on the delegation branch.
