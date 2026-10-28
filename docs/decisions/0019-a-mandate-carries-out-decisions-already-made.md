---
title: "ADR 0019: a mandate carries out decisions already made"
order: 19
description: A person who steps away can issue a mandate so a long session works through the plan's job rows inside terms they typed. The mandate, its cap, its risk tolerance and the acts it reserves are this repository's Buzz policy, a reference another workspace copies; magus ships only two generic host APIs the policy needs, a Stop rule seam and the risk tier as a Buzz call.
tags: [adr, decision, agents, guard, jobs, risk, long-running, policy]
status: proposed
date: 2026-10-28
---

# ADR 0019: a mandate carries out decisions already made

## Context

The PR 625 session stopped for 6 h 50 min overnight, from 04:09 to 10:58 UTC on
2026-10-28, with a plan's job rows queued and no worker running (the session's
retrospective, transcript lines L7640 to L7647).
The root listed four jobs it could fork, said nothing held the landing checkout, and ended
its turn. In that session the only thing that ever woke the root was a worker's
hand-back, so a turn that ended with no worker running was terminal.

Four facts from this tree set the forces:

- Every one of the session's five Stop hooks recorded or proofread. None refused a stop
  (`magus-checkpoint.buzz` always exits 0 and prints nothing).
- The host forbids the model from scheduling itself: the desktop prompt says never to call
  `CronCreate`, `ScheduleWakeup` or `/loop` unasked, so only a person can start a wake.
- The risk tier (`internal/risk`, trivial < mechanical < scoped < full) is read today by
  `affected ci` sizing, MGS3010, CI verdict inheritance and job goals on `ci`. No guard
  rule reads it and Buzz cannot reach it: `Magus#AssessChange` (`risk.go`) has callers
  only in `cmd/magus`.
- A repository's Buzz guard rules (`hack/policy/guard.buzz`, registered with
  `magus\guard.command`) judge commands, spawns and writes. Nothing a repository writes
  is asked about a Stop event; the Go `judgeStop` answers it alone.

Deciding what the root and its workers may do with no one present is opinion. How long a
night is, which tier is safe to integrate unread, and which files count as permission
config differ by repository and by person. Eli, 2026-10-28: the feature is "a very
subjective feature that should just live in our own repository as a reference", which
someone copies into their own repository and edits.

## Decision

The person always drives. A mandate carries out decisions already made and makes none.

The roles are fixed: the person, the guard, the root and its workers. The person's acts
are nouns that are also verbs: they plan (the plan's job rows), they mandate (a mandate),
and they grant (a grant). A mandate raises the bar; a grant lowers one rule. The guard's
verdict on every call is allow, deny or advise, and a job's work ends integrated or held
for review.

<!--diagram:mandate-roles-->

### 1. The policy is this repository's Buzz, and magus ships two seams

| Part                                                                             | Where                                                      | State   |
| -------------------------------------------------------------------------------- | ---------------------------------------------------------- | ------- |
| `magus\guard.stop(fun(req: StopRequest) > GuardVerdict)`: a workspace Stop rule  | Go host API, beside `magus\guard.command`                  | done    |
| `magus\risk.assess(target, opts)`: the proven tier and its evidence for a change | Go host API over `Magus#AssessChange`                      | done    |
| the mandate, the fork, hold and reserved-act rules                               | `hack/policy/mandate.buzz`, wired in `guard.buzz`          | done    |
| holding on a proven tier rather than `full`                                      | `mandate\assess` over `magus\risk.assess`, in `guard.buzz` | done    |
| the Stop rule wired                                                              | `judgeStop` in `guard.buzz`, set in `magusfile.buzz`       | planned |
| issuing, revoking and reporting on a mandate                                     | `hack/dev/issue-mandate.buzz`, run by the person           | done    |
| the guide's "Work a queued plan" row and "Before you step away" section          | `docs/guides/integrations/agents/long-running-sessions.md` | done    |

Both seams are generic: a Stop rule judges any Stop event a repository has an opinion
on, and the risk call answers any script that wants a tier. Neither names mandates. The
existing Stop wiring (`magus-command.buzz` on Claude Code's Stop) already renders a deny
as the host's block reply, so no hook template is added and nothing is rewired.

### 2. The mandate and its explicit issue

A mandate is issued only by a person's command:

```sh
./magus buzz hack/dev/issue-mandate.buzz -- issue --until 07:30 --tolerance mechanical \
  --rows module-layout,go-indexer,naming-lint
```

The root proposes that line, filled from the job store: the plan's rows, the stop
condition, the end time, the tolerance, and the reserved acts printed back. The person
types it, in a terminal or as the host's shell escape. The mandate is one JSON Lines entry
per issue or revoke in the repository's git common directory (`magus/mandate.jsonl`), so
every worktree reads one; the last line wins.

Only the person issues or revokes a mandate. A message from another session, a parent
included, never stands in for that keystroke: the guard denies `hack/dev/issue-mandate.buzz
issue` and `revoke` to every agent, and agent messages carry no person's authority
([ADR 0020](0020-the-person-directs-sessions-report.md)).

A mandated row is a row the mandate names, or a child a worker forks from one, which by
the job rules narrows its parent. A row the mandate does not name is new scope.

### 3. The cap

The end time is local wall-clock time, required, and the only cap enforced in this
version. Past it, the fork rule denies every fork until the person revokes the mandate,
and the Stop rule lets the turn end.

A token budget is secondary. magus counts no cumulative session tokens today (the guard's
cost state reads per-window context sizes), so `issue` refuses `--tokens` with a message
saying no counter exists, rather than accept a knob it cannot obey.

### 4. The risk tolerance

`--tolerance` names the highest tier at which a job that made a call its brief did not
settle may be integrated with no one present. `hack/policy/mandate.buzz` holds the
repository's ceiling as a constant (`mechanical` here), and `issue` refuses a tolerance
above it.

A job is settled when `magus job wait` recorded it `pass` and its exit record lists no
`unresolved_risks`. Anything else is unsettled. A worker's own report can only make a job
unsettled, never settled, so a worker that says nothing gains nothing.

The tier is what `magus\risk.assess` proves for the job's change from its checkpoint:
diff, graph, `go list` and the syntax-tree compare, and anything unproven is `full`. A
settled job is integrated at any tier through its gates. An unsettled job above the
tolerance is held for review: the rule denies the integration fork that names it, the row
stays as it is for the person, and the root goes on with the next row.

<!--diagram:mandate-raises-the-bar-->

The same call, judged with no mandate and under one. A mandate only moves a verdict
toward deny:

| Call                                                     | No mandate             | Under a mandate                       |
| -------------------------------------------------------- | ---------------------- | ------------------------------------- |
| `job fork` of a mandated row or its child                | allow                  | allow                                 |
| `job fork` of a row the mandate does not name            | allow                  | deny: new scope                       |
| an integration fork of an unsettled job above tolerance  | allow                  | deny: held for review                 |
| an integration fork of a settled job                     | allow                  | allow                                 |
| a write under `docs/decisions/` or `.claude/`            | allow                  | deny: reserved                        |
| `git push`, `gh pr merge`, `magus job grant`             | as the other rules say | deny: reserved                        |
| the root's stop with forkable rows and no worker running | deny once, then allow  | deny, until three refusals over a row |

The figure cannot draw a two-column matrix, so it draws only the tier bar and the table
carries the comparison.

### 5. What stays reserved

While a mandate is in force the guard denies, beyond what it denies always:

| Reserved act                    | The rule denies                                                                          |
| ------------------------------- | ---------------------------------------------------------------------------------------- |
| new scope                       | `magus job fork` of a row the mandate does not name, and any `job apply`                 |
| reversing a decision            | writes under `docs/decisions/` and `docs/design/`                                        |
| hook and permission config      | writes to `.claude/`, `.codex/`, `hack/policy/`, `spells/harness/`, `magus.yaml`         |
| a grant                         | `magus job grant` (person-only already; listed so the report names it)                   |
| merge and push                  | `gh pr merge`, `git push` (push-ungated stands; merge is never the root's or a worker's) |
| deleting work not made under it | `magus job rm`, `git branch -D`, `git worktree remove` of a row or branch made before it |

A mandate only adds denials. It lowers no rule, and the guide's "An unattended mode, a
relaxed rule set" refusal stands: with no one present, a point that needs judgment
denies harder.

### 6. The stop rule

The Stop rule refuses the root's Stop when every one of these holds:

- some mandated row is `declared`, not paused, not blocked, and its `depends_on` rows
  passed;
- no row is `running`, so no hand-back wakes the root;
- the mandate is in force (before its end time).

The refusal names the rows and `./magus describe job` for the first. With no mandate in
force the same check refuses once per stop (`stop_hook_active` lets the second stop
through), so a person present can end a turn to ask a question. A person who wants the
session to stop pauses the rows.

Under a mandate a refusal that names the same rows three stops running lets the turn end
and files an attention entry: a fork that keeps failing needs the person, and a loop that
cannot make progress spends tokens on nothing.

### 7. The report

`./magus buzz hack/dev/issue-mandate.buzz -- report` prints, from the job store and the mandate:
rows held for review with their tier and evidence first, rows integrated, rows left and
why (blocked, paused, past the cap), attention entries, and each person-only step left
(push, grant, hook wiring) with its command. The root may propose when to show it from
local time; it never decides that the person is back.

### 8. Enforcement points

| Rule                                        | Point                                                                                            |
| ------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| only the person issues or revokes a mandate | command rule denies `hack/dev/issue-mandate.buzz issue` and `revoke`; write rule denies the file |
| forks stay inside the mandate and the cap   | command rule over `job fork` and `job apply`                                                     |
| an unsettled job above tolerance is held    | command rule over an integration fork, reading `magus\risk.assess`                               |
| reserved acts                               | command and write rules, table above                                                             |
| no idle stop with forkable rows             | `magus\guard.stop` rule                                                                          |
| each rule's verdicts                        | inline Buzz `test` blocks in `hack/policy/mandate.buzz`; Go testify suites for the two seams     |

### Principles checked

- [Human in the loop](../design/principles/human-in-the-loop.md): the mandate is a named
  point the person types, with its terms printed back; what is integrated unread is
  bounded by a tier they chose, and merge, push, grant and config stay theirs.
- [No silent defaults](../design/judgment/no-silent-defaults.md): every field is typed at
  issue; a tolerance above the ceiling and a token budget no counter can obey are refused.
- [Told, never guessed](../design/judgment/told-never-guessed.md): nothing infers that the
  person left. Local time and session history may only propose the mandate.
- [ADR 0013, the guard denies](0013-the-guard-denies.md): each boundary is a
  deny, and the Stop check refuses rather than reminds.
- [Determinism at chosen points](../design/judgment/determinism-at-chosen-points.md): the
  tier and the settled test are computed; nothing judges prose.
- [The host wiring is yours](../design/judgment/the-host-wiring-is-yours.md): the
  opinion is a Buzz file to copy and edit; Go gains seams any repository can use.
- [A refusal carries its reason](../design/judgment/a-refusal-carries-its-reason.md):
  each deny names the mandate term it enforces and the next step (the fork line, the
  person-only command, `report`).
- [Capability is not a reason](../design/principles/capability-is-not-a-reason.md): the
  failure removed is measured, 6 h 50 min idle with the plan's rows queued.
- [The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md):
  the Stop rule refuses through the host's own reply; it never writes the model's prompt.
- [Manual on purpose](../design/judgment/manual-on-purpose.md): work held for review,
  attention entries and every merge wait for the person's keystroke.

## Alternatives

### Infer sleep from local time or the person's usage history

Rejected. It fails both ways: a late night reads as away while the person is reading,
and a nap at 15:00 reads as present. Either wrong guess changes what the guard allows
with no one having said so, which is a silent default and an environment sniff
([Told, never guessed](../design/judgment/told-never-guessed.md),
`TestNoEnvironmentSniffing`). Kept only as a proposal for the mandate's times. Reopen
never as a trigger.

### A magus.yaml tolerance key

Rejected. It puts a subjective knob in the shipped config schema, read by Go for a
feature only this repository's policy implements. The ceiling is a Buzz constant beside
the rule that reads it, and the night's value is typed at issue. Reopen if a second
shipped feature needs the same tolerance.

### Mandates in the Go guard

Rejected for the same reason as the key: the reserved list, the ceiling and the loop
limit are opinions. Go gains the two facts Buzz could not reach. Reopen if every
repository that copies the reference ends up with the same file unchanged.

### A new stop hook template beside `magus-checkpoint.buzz`

Rejected. A shipped template is wired into every host config, `hookTemplates` and
`TestShippedHookConfigsWireTheSameJobs`, for a check most workspaces do not want.
`magus-command.buzz` already runs on Stop and renders a deny as a block. Reopen if a host
runs `magus-command.buzz` on no Stop event.

### A worker-declared list of calls in the exit record

Rejected as the settled test. A worker that omits a call would have it integrated unread.
The `unresolved_risks` field stays, read only to tighten.

### A scheduler or watcher that wakes the root

Rejected, as before: the host owns the clock, and a watcher costs more than the stalls it
catches (record of refusals: a night watcher; the root as a poller). The Stop rule is one
check at stop time, not a poll.

## Consequences

- A queue in prose is invisible to every rule here. A plan's jobs go into the store with
  `job apply` before the mandate is issued, so it can name them.
- A person who wants the session to stop pauses the rows, revokes the mandate or lets the
  end time pass.
- A copy of the reference in another workspace needs a magus with both seams.
- Work held for review piles up for the morning. That is the cost of a tolerance, and the
  report lists it first.

## Open questions

- Whether Claude Code's `!` shell escape and Remote Control from a phone run hooks. If
  `!` runs PreToolUse, the person's own `issue` line is denied as an agent's, and `issue`
  needs a channel only the person writes (a PR review, the push-ungated proposal at L7404).
- Whether a project-scoped `ci` run should ever count as the branch gate (running doc
  entry 277). The report names the gate's scope either way.
- The token counter, if anyone ever wants the budget.
