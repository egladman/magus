---
title: "ADR 0020: the person directs; sessions report"
order: 20
description: The person may direct any session at any time; a parent steers the sessions and workers it started; a sibling or worker reports and never directs. A person's decision made in a child is written to the child's job row, and the guard refuses a fork in that row's area until the forking session has read it.
tags: [adr, decision, agents, guard, jobs, orchestration]
status: proposed
date: 2026-10-28
---

# ADR 0020: the person directs; sessions report

## Context

Eli steers child sessions directly. On 2026-10-28 he renamed "away mode" to "mandate" in a
child session, the one building [ADR 0019](0019-a-mandate-carries-out-decisions-already-made.md),
while its parent kept briefing workers with the old name, because the parent could not see
the decision.

Two facts shape the answer. The host already marks a message from another session as
carrying no person's authority, so a session cannot launder a person's say through another.
And leases already stop two writers on one file, so two sessions directed at once cannot
write over each other. What the parent lacked was not permission but sight of a decision.

## Decision

1. The person may direct any session at any time, without limit.
2. A parent steers the sessions and workers it started.
3. A sibling or worker reports, never directs.

   There is no gate on any of these. The host marks agent messages as carrying no person's
   authority, and leases already stop two writers on one file.

4. The stale-plan fix is visibility. A child appends each decision the person makes there
   to the decision log its parent reads, and the parent re-reads that log before forking
   work in the child's area.

   Its enforcement point today is the orchestration skill, a repo-local
   magus-local-development entry: a prose rule. That is a debt against
   [Enforcement, not pinky promises](../design/principles/enforcement-not-pinky-promises.md)
   and [ADR 0013, the guard denies](0013-the-guard-denies.md), and part 5 pays it.

5. The guard refuses an agent's `job fork` when the child's row carries a person's decision
   newer than the forking session's last read of that row. It never fires on a fork a
   person types. Advice is ignored (ADR 0013), and clearing the refusal takes one cheap
   command, so it never strands work.

   - **Area** is write-path overlap: the fork's write paths intersect the child's live or
     last row's, proven the way fork already proves collisions. A project or a plan is too
     coarse.
   - The design folds into what exists: no new store and no new verb. A decision is an
     entry on the child's job row (who decided, which session, the text, and optional
     narrower scope paths) in the existing JSON Lines job store. A plan's DECISIONS.md
     may be generated from it, never the reverse.
   - **The read** is recorded when the session runs `magus describe job <child>`, kept as a
     per-session mark in the guard's existing session facts. A shell `cat` of a file is
     not a read, because the guard cannot see one reliably.
   - The accepted wrong firing is a child that logs a decision unrelated to the fork while
     its row overlaps. The cost is one describe, seconds, and it does not repeat until the
     next entry; a narrower scope on the entry avoids it. It never fires on
     non-overlapping paths, and never blocks a person.

| Part                                                         | Where                                   | State   |
| ------------------------------------------------------------ | --------------------------------------- | ------- |
| 1 to 3: who directs whom                                     | the host's message wrapper, job leases  | done    |
| 4: the child logs the person's decisions for its parent      | the orchestration skill (prose)         | done    |
| 5: the guard refuses a fork until the newer decision is read | job row decision entries, the fork rule | planned |

### Principles checked

- [Human in the loop](../design/principles/human-in-the-loop.md): the person's say reaches
  any session directly, and a decision they make anywhere binds the forks that follow.
- [Agents propose, humans dispose](../design/judgment/agents-propose-humans-dispose.md): a session that directs another only proposes; the
  person's message is the only one that carries authority.
- [ADR 0013, the guard denies](0013-the-guard-denies.md): part 5 is a refusal, not advice,
  because advice is ignored.
- [Enforcement, not pinky promises](../design/principles/enforcement-not-pinky-promises.md):
  part 4 alone is a prose rule and is named as a debt; part 5 is its enforcement point.
- [Friction that carries meaning](../design/principles/friction-that-carries-meaning.md): nothing gates the person, because limiting which session
  they may talk to carries no meaning they asked for; the one refusal falls on a fork that
  would act on a stale plan.

## Alternatives

### Gate the person to one session

Rejected. It hinders how a developer works, and nothing measured a cost of directing
several sessions.

### Route every person message through the root

Rejected for the same reason, and the root is the dearest context to spend it in.

### A guard rule that lets only the parent message a child

Rejected for now: the host's message wrapper already strips authority from an agent's
message, so the rule would deny nothing that carries weight.

### A separate decision store with its own `decisions ls` verb

Rejected under "fold, don't add": the entries belong to job rows the store already keys.

### Reading a file as the mark

Rejected: the guard cannot see shell reads, so a `cat` of a decision file could not prove
the parent read it.

## Consequences

- A person may change course in whichever session is in front of them; the parent learns
  of it before it next forks in that area.
- A decision entry on a row is a cost a child pays once per decision, and a parent pays one
  describe per newer entry it forks over.
- Until part 5 is built, a parent that skips the re-read acts on a stale plan, as on
  2026-10-28.

## Open questions

- Where a decision goes when the person decides in a session that holds no job row.
