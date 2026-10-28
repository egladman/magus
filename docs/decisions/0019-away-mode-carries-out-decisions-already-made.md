---
title: "ADR 0019: away mode carries out decisions already made"
order: 19
description: A person who steps away can leave a long session working through approved job rows inside a contract they said yes to. The contract, its cap, its risk tolerance and the acts it reserves are this repository's Buzz policy, a reference another workspace copies; magus ships only two generic host APIs the policy needs, a Stop rule seam and the risk tier as a Buzz call.
tags: [adr, decision, agents, guard, jobs, risk, long-running, policy]
status: proposed
date: 2026-10-28
---

# ADR 0019: away mode carries out decisions already made

## Context

The PR 625 session stopped for 6 h 50 min overnight, from 04:09 to 10:58 UTC on
2026-10-28, with approved work queued and no worker running (the session's
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

Deciding what an agent may do with no one present is opinion. How long a night is, which
tier is safe to land unread, and which files count as permission config differ by
repository and by person. Eli, 2026-10-28: away mode is "a very subjective feature that
should just live in our own repository as a reference", which someone copies into their
own repository and edits.

## Decision

The person always drives. Away mode carries out decisions already made and makes none.

### 1. The policy is this repository's Buzz, and magus ships two seams

| Part                                                                             | Where                                                      | State   |
| -------------------------------------------------------------------------------- | ---------------------------------------------------------- | ------- |
| `magus\guard.stop(fun(req: StopRequest) > GuardVerdict)`: a workspace Stop rule  | Go host API, beside `magus\guard.command`                  | planned |
| `magus\risk.assess(target, opts)`: the proven tier and its evidence for a change | Go host API over `Magus#AssessChange`                      | planned |
| the contract, the fork, parking and reserved-act rules                           | `hack/policy/away.buzz`, wired in `guard.buzz`             | done    |
| the Stop rule wired, and parking on a proven tier rather than `full`             | `judgeStop` in `guard.buzz`, over the two seams above      | planned |
| starting, ending and reporting on a window                                       | `hack/dev/away.buzz`, run by the person                    | done    |
| the guide's "Work a queued plan" row and "Before you step away" section          | `docs/guides/integrations/agents/long-running-sessions.md` | done    |

Both seams are generic: a Stop rule judges any Stop event a repository has an opinion
on, and the risk call answers any script that wants a tier. Neither names away mode. The
existing Stop wiring (`magus-command.buzz` on Claude Code's Stop) already renders a deny
as the host's block reply, so no hook template is added and nothing is rewired.

### 2. The contract and its explicit start

A window starts only from a person's command:

```sh
./magus buzz hack/dev/away.buzz -- start --until 07:30 --tolerance mechanical \
  --rows module-layout,go-indexer,naming-lint
```

The session proposes that line, filled from the job store: the approved rows, the stop
condition, the end time, the tolerance, and the reserved acts printed back. The person
types it, in a terminal or as the host's shell escape. The guard denies the command to an
agent, since starting a window is the decision. The contract is one JSON Lines entry per
start or end in the repository's state directory; the last line wins.

An approved row is a row the contract names, or a child a worker forks from one, which by
the job rules narrows its parent. A row the contract does not name is new scope.

### 3. The cap

The end time is local wall-clock time, required, and the only cap enforced in this
version. Past it, the fork rule denies every fork and the Stop rule lets the turn end.

A token budget is secondary. magus counts no cumulative session tokens today (the guard's
cost state reads per-window context sizes), so `start` refuses `--tokens` with a message
saying no counter exists, rather than accept a knob it cannot obey.

### 4. The risk tolerance

`--tolerance` names the highest tier at which a job that made a call its brief did not
settle may land with no one present. `hack/policy/away.buzz` holds the repository's
ceiling as a constant (`mechanical` here), and `start` refuses a tolerance above it.

A job is settled when `magus job wait` recorded it `pass` and its exit record lists no
`unresolved_risks`. Anything else is unsettled. A worker's own report can only make a job
unsettled, never settled, so a worker that says nothing gains nothing.

The tier is what `magus\risk.assess` proves for the job's change from its checkpoint:
diff, graph, `go list` and the syntax-tree compare, and anything unproven is `full`. A
settled job lands at any tier through its gates. An unsettled job above the tolerance is
parked: the rule denies the integration fork that names it, the row stays as it is for
the person, and the queue continues with the next row.

### 5. What stays reserved

While a window is open the guard denies, beyond what it denies always:

| Reserved act                    | The rule denies                                                                                 |
| ------------------------------- | ----------------------------------------------------------------------------------------------- |
| new scope                       | `magus job fork` or `job apply` of a row the contract does not name                             |
| reversing a decision            | writes under `docs/decisions/` and `docs/design/`                                               |
| hook and permission config      | writes to `.claude/`, `.codex/`, `hack/policy/`, `spells/harness/`, `magus.yaml`                |
| a job grant                     | `magus job grant` (person-only already; listed so the report names it)                          |
| merge and push                  | `gh pr merge`, `git push` (push-ungated stands; merge is never an agent's)                      |
| deleting work it did not create | `magus job rm`, `git branch -D`, `git worktree remove` of a row or branch older than the window |

Away mode only adds denials. It lowers no rule, and the guide's "An unattended mode, a
relaxed rule set" refusal stands: with no one present, a point that needs judgment
denies harder.

### 6. The Stop rule

The Stop rule refuses a root's Stop when every one of these holds:

- some approved row is `declared`, not paused, not blocked, its `depends_on` rows passed,
  and it overlaps no live row's write paths;
- no row is `running`, so no hand-back will wake the root;
- the window is open (before its end time).

The refusal names the rows and the `job fork` line for the first. With no window open the
same check refuses once per stop (`stop_hook_active` lets the second stop through), so a
person present can end a turn to ask a question. A person who wants the session to stop
pauses the rows.

Inside a window a refusal that names the same rows three stops running lets the turn end
and files an attention row: a fork that keeps failing needs the person, and a loop that
cannot make progress spends tokens on nothing.

### 7. The morning report

`./magus buzz hack/dev/away.buzz -- report` prints, from the job store and the contract:
rows landed, rows parked with their tier and evidence, rows left and why (blocked,
paused, past the cap), attention rows, and each person-only step left (push, grant, hook
wiring) with its command. The session may propose when to show it from local time; it
never decides that the person is back.

### 8. Enforcement points

| Rule                                       | Point                                                                                           |
| ------------------------------------------ | ----------------------------------------------------------------------------------------------- |
| a window starts only from a person         | command rule denies `hack/dev/away.buzz start` to an agent; write rule denies the contract file |
| forks stay inside the contract and the cap | command rule over `job fork` and `job apply`                                                    |
| an unsettled job above the tolerance parks | command rule over an integration fork, reading `magus\risk.assess`                              |
| reserved acts                              | command and write rules, table above                                                            |
| no idle stop with forkable rows            | `magus\guard.stop` rule                                                                         |
| each rule's verdicts                       | inline Buzz `test` blocks in `hack/policy/away.buzz`; Go testify suites for the two seams       |

### Principles checked

- [Human in the loop](../design/principles/human-in-the-loop.md): the window is a named
  point the person types, with its terms printed back; what lands unread is bounded by a
  tier they chose, and merge, push, grant and config stay theirs.
- [No silent defaults](../design/judgment/no-silent-defaults.md): every field is typed at
  start; a tolerance above the ceiling and a token budget no counter can obey are refused.
- [Told, never guessed](../design/judgment/told-never-guessed.md): nothing infers that the
  person left. Local time and session history may only propose the window.
- [ADR 0013, the guard denies](0013-the-guard-denies.md): each boundary is a
  deny, and the Stop check refuses rather than reminds.
- [Determinism at chosen points](../design/judgment/determinism-at-chosen-points.md): the
  tier and the settled test are computed; nothing judges prose.
- [The host wiring is yours](../design/judgment/the-host-wiring-is-yours.md): the
  opinion is a Buzz file to copy and edit; Go gains seams any repository can use.
- [A refusal carries its reason](../design/judgment/a-refusal-carries-its-reason.md):
  each deny names the contract term it enforces and the next step (the fork line, the
  person-only command, `report`).
- [Capability is not a reason](../design/principles/capability-is-not-a-reason.md): the
  failure removed is measured, 6 h 50 min idle with work approved.
- [The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md):
  the Stop rule refuses through the host's own reply; it never writes the model's prompt.
- [Manual on purpose](../design/judgment/manual-on-purpose.md): parked work, attention
  rows and every merge wait for the person's keystroke.

## Alternatives

### Infer sleep from local time or the person's usage history

Rejected. It fails both ways: a late night reads as away while the person is reading,
and a nap at 15:00 reads as present. Either wrong guess changes what the guard allows
with no one having said so, which is a silent default and an environment sniff
([Told, never guessed](../design/judgment/told-never-guessed.md),
`TestNoEnvironmentSniffing`). Kept only as a proposal for the window's times. Reopen
never as a trigger.

### A magus.yaml tolerance key

Rejected. It puts a subjective knob in the shipped config schema, read by Go for a
feature only this repository's policy implements. The ceiling is a Buzz constant beside
the rule that reads it, and the night's value is typed at start. Reopen if a second
shipped feature needs the same tolerance.

### Away mode in the Go guard

Rejected for the same reason as the key: the reserved list, the ceiling and the loop
limit are opinions. Go gains the two facts Buzz could not reach. Reopen if every
repository that copies the reference ends up with the same file unchanged.

### A new Stop hook template beside `magus-checkpoint.buzz`

Rejected. A shipped template is wired into every host config, `hookTemplates` and
`TestShippedHookConfigsWireTheSameJobs`, for a check most workspaces will not want.
`magus-command.buzz` already runs on Stop and renders a deny as a block. Reopen if a host
runs `magus-command.buzz` on no Stop event.

### A worker-declared list of calls in the exit record

Rejected as the settled test. A worker that omits a call would land it unread. The
`unresolved_risks` field stays, read only to tighten.

### A scheduler or watcher that wakes the root

Rejected, as before: the host owns the clock, and a watcher costs more than the stalls it
catches (record of refusals: a night watcher; the root as a poller). The Stop rule is one
check at stop time, not a poll.

## Consequences

- A queue in prose is invisible to every rule here. A plan's jobs go into the store with
  `job apply` before the window starts, so the contract can name them.
- A person who wants the session to stop pauses the rows or lets the end time pass.
- A copy of the reference in another workspace needs a magus with both seams.
- Parked branches pile up for the morning. That is the cost of a tolerance, and the
  report lists them first.

## Open questions

- Whether Claude Code's `!` shell escape and Remote Control from a phone run hooks. If
  `!` runs PreToolUse, the person's own `start` line is denied as an agent's, and `start`
  needs a channel only the person writes (a PR review, the push-ungated proposal at L7404).
- Whether a project-scoped `ci` run should ever count as the branch gate (running doc
  entry 277). The report names the gate's scope either way.
- The token counter, if anyone ever wants the budget.
