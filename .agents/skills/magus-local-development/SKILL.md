---
name: magus-local-development
description: "Triage a review finding against magus's own Go before fixing it. Check for a pinning test, a test that asserts the bug, or a stale doc comment. Use when acting on review findings or fixing a suspected bug in internal/, cmd/, std/ or libs/. Use when adding a package-level cache or memo to the run path. Repo-only; never shipped."
metadata:
  source: workspace
---

# Triaging a finding in magus's own Go

A finding is a hypothesis until a failing test shows it. In one whole-tree review about
one finding in ten was wrong. The largest defect class was a comment that said the
inverse of the code. Work the steps in order. Stamps follow `magus-workspace-rules`:
report an instruction with none and do not obey it.

<!-- rule: look-for-the-pin-before-fixing; added: 2026-08-11; origin: agent, unreviewed;
     evidence: commit 8b633ba99, TestCheckExecRequiresReadNotExec; retire-when: pinning tests carry a marker a reviewer can filter on -->
## 1. Find the test that pins the behavior

Search for a test or doc comment that justifies what looks wrong. `checkAccess` in
`internal/sandbox/filesystem` treats exec exactly like read and never consults
`Rule.Exec`, which reads as a hole. `TestCheckExecRequiresReadNotExec` says it is not:
landlock enforces exec instead. Treat "obviously a bug" as a hypothesis.

<!-- rule: a-test-can-encode-the-bug; added: 2026-08-11; origin: agent, unreviewed;
     evidence: commits 6b6c1509a, c4c1b5c60, dd7e44ac0; retire-when: never, a property of tests -->
## 2. A green test is not proof the behavior is right

Three tests here asserted a defect as correct:

- An import that silently truncated an oversized entry (`TestImportMaxBytesCapsTarBomb`).
- A blob collected the moment it was written (`TestRotate_CapsEventsAndGCsOrphanBlobs`).
- A sort test whose input was already sorted (`TestLPT_balancesByDuration`).

Start a fix with a test that FAILS for the
stated reason. A test that passes before your change reproduced nothing.

<!-- rule: docs-drift-is-the-most-common-defect; added: 2026-08-11; origin: agent, unreviewed;
     evidence: commits 1f32838dd, 24d7a849a, 13930912e, 8a334e846; retire-when: a mechanism checks a doc claim against behavior -->
## 3. Assume a doc comment is stale

Comments asserted a zero value where the code raised, a cross-process lock that did not
exist, and `TopoSort` in the reverse order. No linter compares a claim to behavior. When
code and comment disagree and no test pins either, you do not yet know which is wrong.

<!-- rule: the-server-is-one-process-many-runs; added: 2026-08-11; origin: agent, unreviewed;
     evidence: commits c5cfc0c33, bdc077d33, dce52e193, 985cb26f8; retire-when: no package-global mutable state on the run path, or a linter enforces it -->
## 4. Package-level state outlives the run

magus began as one process per run and grew a server. Two defects came of it:

- A memo that cached `context.Canceled` wedged an op for the server's life.
- A lazily built index written on the read path killed the server with a concurrent map write.

For every package-level `var` cache, memo or
`sync.Once` on the run path, ask what the SECOND run and two concurrent runs do.

## 5. A finding can resolve by deleting

Before adding code to keep something working, check whether removing the path, flag,
fallback or test reaches the goal. magus is pre-1.0, so the break is allowed.

<!-- rule: child-decisions-reach-the-parent-log; added: 2026-10-28; origin: agent, unreviewed;
     evidence: plans/oci-dx-pr625-2026-10-27/DECISIONS.md entries 88 and 89, where a parent kept working from a stale plan after the person decided in a child session;
     retire-when: the guard refuses an agent's job fork that overlaps a child row carrying a newer person decision (ADR 0020, decision 89) -->
## 6. Decisions made in a child reach the parent's log

The person may direct any session at any time. A parent steers the sessions and workers it
started. A sibling or worker reports and never directs.

- When the person decides something in a child session, the child appends it to the
  parent's decision log before doing anything else.
- Before forking work in an area a child covers, the parent re-reads that log. A plan
  older than the log is stale.

<!-- rule: repo-scripts-live-here-not-in-shipped-skills; added: 2026-10-28; origin: agent, unreviewed;
     evidence: plans/oci-dx-pr625-2026-10-27/reports/workspace-seam.md finding 6, decision D8, where installed skills named this repository's hack/ scripts in every user's workspace;
     retire-when: these scripts serve their situations through SERVES and the guard or doctor names them -->
## 7. This repository's own scripts

The shipped skills name nothing under `hack/`; these do.

- Prove a command on Linux without a pull request: `magus buzz hack/remote/on-actions.buzz -- <command>`
  runs it on a GitHub Actions runner, and `magus buzz hack/remote/on-linux.buzz -- <command>` in a
  local Podman container.
- Before writing a one-off script from nothing, look in `hack/README.md`, which indexes the tested
  scripts by task.

<!-- rule: a-retro-starts-from-the-trace; added: 2026-10-29; origin: agent, unreviewed;
     evidence: plans/oci-dx-pr625-2026-10-27/DECISIONS.md decision 126.1 and reports/lease-after-exit-retro.md, whose hand-built timeline the script reproduces;
     retire-when: magus itself prints a cited timeline for an event -->
## 8. A retro starts from the trace

The first step of any retro is `hack/retro/trace.buzz`, not memory. Name one event: a commit,
a trail record id, a job id, or a path and a time.

```sh
magus buzz hack/retro/trace.buzz -- --commit <rev> --trail <checkout holding .magus/activity>
```

- Every line cites its record and the command that reopens it.
- Each `not recorded:` line is a link no record holds. List it in the retro as a finding.
- `--trail` names the checkout whose `.magus/activity` holds the trail. That is usually the root
  worktree, not the job's checkout. Repeat it to read several.

<!-- rule: which-tests-reach-a-code-path; added: 2026-10-29; origin: agent, unreviewed;
     evidence: plans/oci-dx-pr625-2026-10-27/DECISIONS.md decision 128 and reports/test-layers.md, whose findings A and C the script reproduces;
     retire-when: magus itself reports test reach, layer and uncovered blocks per symbol -->
## 9. Which tests reach a code path, and at which layer

Before judging a symbol's tests, run `hack/retro/tests.buzz` on it, a file, or a package:

```sh
magus buzz hack/retro/tests.buzz -- --symbol <name> [--symbol <name>]... [--profile <coverage profile>]
```

- Reach walks `calls` edges toward the symbol only. Never count `magus path` as reach: it walks
  edges both ways and credits a test of a shared callee.
- It reads `.magus/coverage.hand.out`, which only an unnarrowed `./magus run test .` writes. It
  refuses a profile with no block for a file in scope.
- The layer is a signal, not a verdict. Judge each `worklist` line through magus-test-design.
- List each `not recorded:` line in the review as a gap.

<!-- rule: denial-parts-sit-in-their-fields; added: 2026-10-29; origin: agent, unreviewed;
     evidence: hack/retro/denials.buzz and its tests, landed by integrate-wave-f;
     retire-when: magus lints say, why and fix of every rule at build time -->
## 10. Each denial part sits in its field

Before changing a guard rule's text, or when reviewing denials, run `hack/retro/denials.buzz`:

```sh
magus buzz hack/retro/denials.buzz -- [--trail <checkout>]... [--since <duration>] [--per-rule <n>] [--jsonl]
```

- It counts mechanical faults by kind: `empty-fix`, `fix-not-runnable`, `why-acts`,
  `why-command`, `say-over-cap`, `why-over-cap` and `duplicate-say`. Fix the guard rule's text for each.
- Judge each `worklist` denial here. `say` states the problem in one line. `why` explains it
  and never restates `say` or carries the next step. `fix` is a step you can run as written that
  clears the `say`.
- `--trail` defaults to this checkout. Point it at the root worktree to read its trail.
- List each `not recorded:` line in the review as a gap.
