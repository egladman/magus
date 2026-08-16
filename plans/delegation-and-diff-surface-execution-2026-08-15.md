# Execution: delegation surface + diff surface, delegated

Written 2026-08-15 on `feat/delegation-surface-plan-fd1f16`
(worktree `.claude/worktrees/delegation-surface-plan-fd1f16`, base `60dc91519`).

Two workstreams run in parallel, each in its own worktree, orchestrated from
this session with opus/sonnet subagents. This session deliberately runs the
doctrine it is shipping: the ledger below carries the base revision, the
read-only exclusion, and the pass/fail/no-return vocabulary that steps 1-6
add to the skill.

- Workstream A implements
  [delegation-surface-implementation-plan.md](delegation-surface-implementation-plan.md)
  (rationale in
  [delegation-lessons-from-deepseek-harness.md](delegation-lessons-from-deepseek-harness.md)),
  here on `feat/delegation-surface-plan-fd1f16`. Both plan files were copied
  from the uncommitted tree in `.claude/worktrees/beep-f7225d` and are
  committed here so they stop living in one working tree.
- Workstream B continues the diff surface per
  `plans/diff-surface-implementation-handoff.md` ON
  `feat/pwa-diff-viewer-replay-afea09` in worktree `wonderful-maxwell-80473c`,
  never a fresh branch. That branch contains `60dc91519`, so its binary can
  bootstrap this worktree (and did).

## Decisions made in Eli's absence

Each is reversible and flagged for review:

1. **Base revision primitive (plan prerequisite).** SUPERSEDED by Eli
   mid-session: mint "checkpoint" as a first-class concept. `magus vcs
   checkpoint` + `magus_vcs_checkpoint` resolve revision + branch + dirty +
   uncommitted-patch digest (the AsOf shape), minting no VCS state. The
   ledger column becomes Checkpoint and cites that command; `magus graph
   diff --rev <revision>` stays the graph-level reopen. My earlier stand-in
   choice (bare revision id, no new concept) held for about two hours.
2. **TUI entry: explicit `magus diff --tui` first.** Auto-on-TTY is the more
   "CLI reads context" answer but silently changes the default of a command
   whose scrollable report the unix-graybeard persona liked. The flag is
   additive and the auto flip stays open as a one-line change once Eli picks.
3. **Eval gap recorded, not closed.** The delegation skill has no eval task
   (`evals/tasks/` holds only magus-run and magus-vcs-hygiene) and the
   `skill-evals` audit job is muted (`if: false` since 2026-08-03, targets
   commented out in `evals/magusfile.buzz`). Steps 1-6 therefore ship
   unmeasured. Reviving a deliberately muted paid job is Eli's call; writing a
   delegation eval task is real work that should ride that revival.

## Ledger

Budget: max 4 concurrently active workers, depth 1 (no nested delegation),
opus for design/engine units, sonnet for scout/mechanical. Git stays with the
orchestrator; no worker touches generated output; regeneration happens once
per workstream at integration.

| Unit | Worktree | Goal | Owned paths | Base revision | Tier | State |
|---|---|---|---|---|---|---|
| A2 skill steps 1-6 | delegation-surface-plan | 6 doctrine edits, ~25 rendered lines | cmd/magus/skills/magus-delegate-multi-agent/SKILL.md | 60dc91519 | opus | running |
| A3 describe-file facts | delegation-surface-plan | output globs + dep edges + affinity facts, never verdicts | cmd/magus/describe*.go + backing internals + tests | 60dc91519 | opus | running |
| A4 regen+gate+commit | delegation-surface-plan | 3 generated copies in same commit; affected ci --no-default-charms | (orchestrator) | - | root | blocked on A2 |
| A5 collision shrink | delegation-surface-plan | SKILL.md:171-183 consumes A3 fields and SHRINKS | SKILL.md | - | root | blocked on A3+A4 |
| B0 scout | wonderful-maxwell | file:line map for B2/B3 specs | none (read-only, excluded from collision analysis) | aa3ef9767 | sonnet | running |
| B1 words port | wonderful-maxwell | internal/diff/words.go + pinned parity test | internal/diff/words*.go | aa3ef9767 | opus | running |
| B2 TUI core | wonderful-maxwell | magus diff --tui: hunk stream, nav, fold, UNRANKED, viewed marks | cmd/magus/diff.go + new TUI pkg | - | opus | blocked on B0 |
| B3 MCP projection+delta | wonderful-maxwell | projection param on magus_diff ops; changed-since-viewed | MCP diff op files, additive only | - | opus | blocked on B0 |
| B4 syntax palette | wonderful-maxwell | one source for console/docs syn tokens | console/ + docs/ palette files | - | sonnet | queued (cap) |
| B5 rotate+gate+commit | wonderful-maxwell | token rotation, serial integration, gate, commits, handoff update | (orchestrator) | - | root | blocked on B1-B4 |

Every row terminates as pass, fail, or no-return; silence is not a pass.

## Sequencing

1. Round 1 (running): A2, A3, B0, B1.
2. A2 returns: orchestrator reviews the diff against 60dc91519, regenerates
   (`./magus agent install .claude/skills --force`;
   `MAGUS_UPDATE_EVAL_FIXTURES=1` fixtures test; docs generate), gates,
   commits skill+regen together (A4).
3. A3 returns: review, test, commit engine work; then the orchestrator writes
   the SKILL.md:171-183 shrink citing the real field names, regenerates and
   commits again (A5).
4. B0 returns: spawn B2 and B3 with the map; spawn B4 when a slot frees.
5. B1..B4 return: serial integration in wonderful-maxwell with per-unit diff
   review against aa3ef9767; stop daemon; rotate mcp_token (leaked, live,
   confirmed by the handoff); full gate `magus affected ci
   --no-default-charms`; restart daemon; logical commits; update
   `plans/diff-surface-implementation-handoff.md` (B5).

## Deferred from the diff handoff, with reasons

- **Gate mode**: threshold semantics are undesigned; wrong to guess under
  delegation. Needs a decision on what failing means (unviewed hunks? reach
  threshold? affected targets red?).
- **Bus-factor / knowledge-loss join**: real feature, not started; the
  ownership lens exists and is unread by the diff. Next session candidate.
- **Undeclared-write diagnostic**: deliberately linked to workstream A's
  thesis (describe-file facts are only as honest as declared outputs). Do it
  when the fold's completeness matters; cross-referenced in both plans.
- **Buzz in WASM**: blocked on the CSP decision (`'wasm-unsafe-eval'` in
  `internal/service/console/static.go`); scoped in the handoff already.
- **Skill step 8 (supersedes edges on notes)**: the plan itself defers it to
  the notes-store work.

## Gates

Per workstream, before any commit: `magus affected ci --no-default-charms`
from the worktree root (known environmental failure: the doctor console check
needs a running daemon; lint findings from sibling worktrees are filtered by
path). Unit-level loops use narrow `go test` per the repo rules.
