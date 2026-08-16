# Plan: delegation surface changes

Written 2026-08-15 on `feat/multi-agent-delegation-context-2fbbef`
(worktree `.claude/worktrees/beep-f7225d`), based on `60dc91519`.

Contractual companion to
[delegation-lessons-from-deepseek-harness.md](delegation-lessons-from-deepseek-harness.md),
which holds the rationale. This file holds only what to change and how to know
it worked.

## Scope

Steps 1 through 6 edit one file:
`cmd/magus/skills/magus-delegate-multi-agent/SKILL.md`, 251 lines, a template
with `{{if .Full}}` branches. Combined budget: about 25 rendered lines across
both variants. If they do not fit, drop the lowest-value step rather than
compressing all six.

Step 7 is engine work and can land separately. Step 8 is deferred.

## Prerequisite

Step 1 names a VCS primitive that may not exist. magus has nothing called a
checkpoint: `types/vcs.go` mentions the word only as an example of an annotated
tag that fails to parse. What exists is `RevisionExporter`, `Commit`, and
`magus graph diff`. Settle with Eli which of those the ledger column should name
before writing step 1, because the skill must name a resolvable command.

RESOLVED 2026-08-15, by Eli: mint the concept properly. `magus vcs checkpoint`
(CLI, folded into the vcs namespace) plus the `magus_vcs_checkpoint` MCP twin;
identity is revision + branch + dirty + uncommitted-patch digest (the AsOf
shape). A checkpoint resolves and records, it never mints VCS state. The ledger
column adopts the term once the command exists in the same branch.

## Steps

### 1. Add a base revision to the ledger row

Change: add a `Base revision` column to the table at SKILL.md:189 and require
the root to record the revision each unit was handed.

Files: SKILL.md (ledger section, and the Integrate and verify step 1 that reads
the row).

Check: both rendered variants show the column; the worked example names a
resolvable revision, not a placeholder.

### 2. State that owned and forbidden paths are not a boundary

Change: one sentence in the ledger section saying the columns are prompt text,
and that step 1 of Integrate and verify is where the boundary is actually
checked.

Files: SKILL.md (ledger section).

Check: a reader of the simple variant can state where enforcement happens.

### 3. Give read-only units an abbreviated row

Change: split tool surface from model tier. State that a read-only unit gets a
read-only surface where the host has one, carries an abbreviated ledger row, and
is excluded from the collision analysis entirely.

Do not name host agent types. SKILL.md:110 already maps work to provider
capabilities without assuming model names; hold the same line for tool surface.

Files: SKILL.md (tier table at :112, ledger section, collision section).

Check: the collision section says read-only units are excluded; the ledger
section says which columns they omit.

### 4. Forbid retrofitting constraints onto a running worker

Change: one sentence in the course-correction section (SKILL.md:225): a worker
keeps the constraints it was handed, and a tightening requires cancel and
respawn, not a message.

Files: SKILL.md (course-correction section).

Check: present in both variants.

### 5. State the foreground blocking test

Change: block only when the next action requires the worker's result.

Try to REPLACE SKILL.md:206-208 rather than add to it; those lines gesture at
the same rule negatively and this is the positive form.

Files: SKILL.md.

Check: net line count does not grow.

### 6. Require every ledger row to terminate

Change: silence is not a pass. The root records which of pass, fail, or
no-return happened for each row.

Files: SKILL.md (ledger section or Integrate and verify).

Check: the failure vocabulary distinguishes no-return from failed criteria.

### 7. Expose disjointness facts through describe file

Change: fold into `magus describe file`, do not add a command.

First determine what `magus describe file <path>... -o json` already returns. If
it already carries the owning project and declaring target, the missing field is
the declared output globs. Add only what is missing.

Emit facts, never a verdict: overlaps, dependency edges, affinity evidence. The
root decides.

Files: whatever backs `describe file`, plus its generated output and docs. Then
SKILL.md:171-183.

Check: the collision section of the skill SHRINKS. If it does not shrink, the
field went to the wrong place.

### 8. Decision notes with supersedes edges

Deferred. Process question, not a code change. Raise it against the notes-store
work rather than doing it here.

## Regeneration

Three generated copies must land in the SAME commit as any SKILL.md edit:

- `.claude/skills/magus-delegate-multi-agent*` (stamped, checked by
  `magus graph verify`): `magus agent install .claude/skills --force`
- `evals/fixtures/{full,simple}/.claude/skills/...` (pinned by
  `cmd/magus/evalfixtures_test.go`), BOTH variants:
  `MAGUS_UPDATE_EVAL_FIXTURES=1 magus run go::go-test . -- -run TestEvalFixturesMatchTheEmbeddedSkills`
- `docs/reference/skills/magus-delegate-multi-agent.md`, rendered by
  `cmd/magus-skilldocs` through the docs project's `generate`.

## Gate

`magus affected ci --no-default-charms`, so `generate` runs as a pure drift gate.
A skill edit that forgets one of the three copies fails there rather than in CI.

## Verification

Prompt-level doctrine is checked by the eval corpus, not by review. The
`skill-evals` job in `audit.yaml` measures the rendered bodies, and the fixtures
are regenerated here, so the corpus picks up the new text by construction.

Confirm first that the delegation skill is actually covered by an eval. If it is
not, that gap is its own step and steps 1 through 6 ship unmeasured.

## Out of scope

Host-owned. A skill can only ask, so do not write doctrine for them.

- Policy inheritance stamped into a child's session log.
- Scoped tool registries and per-child persona installed before the first
  request.
- Mid-flight child reports that wake a parked parent.
- Depth caps enforced at every start. SKILL.md:86 is honor-system text and stays
  that way.
