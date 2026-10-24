---
title: Long-running sessions
description: Which host scheduler keeps an agent session going for hours (Claude Code loops and goals, desktop and cloud scheduled tasks, Codex, Cursor, GitHub Actions), the magus commands each wake runs, the cloud risk of an unguarded clone, and what magus leaves out.
tags:
  [
    agents,
    scheduling,
    loop,
    goal,
    scheduled tasks,
    cloud,
    guard,
    session,
    attention,
  ]
---

# Long-running sessions

Work that outlasts one sitting needs a clock, a machine, a credential and a
transcript. The host owns all four. magus owns none of them and calls no model,
so it has no scheduler: [the host wiring is yours](../../../doctrine.md#the-host-wiring-is-yours).
What magus gives a long-running session is the answer to the question each wake
asks, which is whether the work is done and whether it broke anything.

The shape that holds up is one wake, one fixed set of commands, and one stop
condition magus can prove.

## What each wake runs

Pick the wake by the work, then pick the host that fires it.

| Work                                | Each wake runs                                                                                         | Stop when                                          |
| ----------------------------------- | ------------------------------------------------------------------------------------------------------ | -------------------------------------------------- |
| Babysit open pull requests          | `./magus buzz hack/ci/pull-requests.buzz -- status`, then `-- apply`                                   | every record reads `merged`                        |
| Let a fanned-out tree finish        | `./magus session attention`, `./magus ls jobs`, then `./magus job wait <job>` for each finished holder | no job is running and `./magus affected ci` passes |
| Report on the workspace (read-only) | `./magus affected ci`, then `./magus describe` on what changed                                         | the report is written and nothing is pushed        |
| Read it back later                  | `./magus session --brief`, `./magus session attention`, `./magus ls jobs`                              | a person has read it                               |

Three rules keep a wake cheap and safe:

- The wake reads state and acts on what magus prints. It never derives a pull
  request state or a job state itself.
- Only the root session schedules. A worker's prompt outlives its lease, and a
  worker has no one to ask. [Jobs and leases](leases.md) covers the split.
- Anything that carries a `merge` field, pushes, or spends money is a person's
  act. The wake hands the command over and stops
  ([human in the loop](../../../doctrine.md#human-in-the-loop)).

A root-only block, such as a stale `./magus` after a merge, reaches the root as
one attention row. The wake clears what it can (rebuild `./magus`, rebind a
lease) and leaves the rest. The daemon has no hot reload, so a rebuilt binary
needs `./magus server stop` then `start`.

## Host schedulers

| Host feature                            | Fires                                                                                         | Runs on                                    | Notes for magus                                                                                                                                 |
| --------------------------------------- | --------------------------------------------------------------------------------------------- | ------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| Claude Code `/loop` with an interval    | cron through `CronCreate`, one minute floor, jittered; between turns only; expires in 7 days  | your machine, the open session             | The guard reads the `prompt` as a prompt queued for the same session, not a spawn.                                                              |
| Claude Code `/loop` without an interval | `ScheduleWakeup`, one minute to one hour, chosen per iteration; `stop: true` ends it          | the same session                           | Not restored by `--resume`. Past the prompt-cache window a wake re-pays the prompt; `./magus session ls` prints each window.                    |
| Bare `/loop` with `.claude/loop.md`     | the file is re-read each iteration                                                            | the same session                           | This repository's file points at the pull request chain above.                                                                                  |
| Claude Code `/goal`                     | a Stop hook; a small model judges a condition after each turn                                 | the same session                           | Write the condition as a magus verdict ("`./magus affected ci` passes and `./magus ls jobs` shows nothing running"), so the judge reads a fact. |
| Desktop scheduled tasks                 | hourly, daily, weekly or cron, one hour floor; one catch-up run after sleep                   | your machine, a fresh session per run      | Set the task's permission mode to Auto. Manual mode stalls until someone approves.                                                              |
| Cloud scheduled tasks                   | a schedule, an API call or a GitHub event, one hour floor                                     | the vendor's cloud, a fresh clone each run | See [the cloud risk](#the-cloud-risk).                                                                                                          |
| Codex scheduled tasks                   | recurring rules; an in-chat task resumes its chat                                             | your machine or an isolated worktree       | The app must run and the machine be on. Reads the same `AGENTS.md` and skills.                                                                  |
| Cursor Automations                      | a schedule or an event such as a webhook                                                      | a cloud VM per agent                       | Same cloud risk. Opens a pull request, which a person merges.                                                                                   |
| GitHub Actions `schedule`               | best effort; delayed or dropped under load, worst at the top of the hour; default branch only | a hosted runner                            | No agent. Run `./magus run` targets directly, and pick an off-peak minute as `audit.yaml` does.                                                 |

A host's limits change faster than this page. The Claude Code, desktop and cloud
rows come from the host's documentation as of 2026-10-23; the Codex and Cursor
rows come from secondary sources and are unverified.

## The cloud risk

A cloud task starts from a fresh clone. The clone has no `./magus`, and the
[guard](guard.md) fails open when no magus binary is reachable: the hooks judge
nothing, so the session is unguarded. Leases, denials and the stall rule are all
absent, and the host's auto mode runs what the model asks.

Fix it in the environment's setup script, not in the prompt. The script builds or
downloads `./magus` and places it at the checkout root before the session starts,
then `./magus doctor` confirms the hooks are wired. Without that line, treat the
run as read-only and give it no credential that can push.

A green status on a cloud run means the session finished, not that the work
succeeded. Read the result with `./magus session --brief` after fetching the
branch it pushed.

## Reading it back

Attention rows are what piles up while no one watches. `./magus session ls`
prints how many are open, and `./magus session attention` lists them, each with
its exec line. `./magus session --brief` is the first read of a new sitting.
Disposing of a row stays a person's act
([manual on purpose](../../../doctrine.md#manual-on-purpose)).

## What magus leaves out

Each row is a decision, not a gap. The decision records and the
[record of refusals](../../../doctrine.md#a-record-of-refusals) hold the
evidence; reopen one only with evidence they did not have.

| Not built                                                                   | Why                                                                                                                                | Where it is decided                                                                                                                                                     |
| --------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A `schedule` verb, a cron, or an `--at` flag                                | the host owns the clock, and a scheduler that launches sessions calls a model                                                      | [Capability is not a reason](../../../doctrine.md#capability-is-not-a-reason); [ADR 0005](../../../decisions/0005-deterministic-answers.md) section 7                   |
| Anything that starts, resumes, prompts or pays for a model session          | magus never calls a model                                                                                                          | Record of refusals: a model adapter; an agent loop in the Go SDK                                                                                                        |
| A night watcher of any size, or the root as a poller                        | the watcher costs more than the stalls it catches, and polling spends the dearest tokens on the cheapest work                      | Record of refusals: a model that watches workers for stalls; the root as the stall watcher                                                                              |
| A daemon sweep for idle workers                                             | not built until measured: the host's stop hooks reach `magus session notify` and the job store's stale-after covers the long tail  | Record of refusals; [ADR 0009](../../../decisions/0009-cost-profiles-for-agent-sessions.md)                                                                             |
| Auto-disposing attention rows, or a stall message that carries the fix      | disposing is a rep worth keeping, and handing over the answer makes a cheap model a typist                                         | [Manual on purpose](../../../doctrine.md#manual-on-purpose); Record of refusals: a stall message that gives the worker the answer                                       |
| An unattended mode, a relaxed rule set, or sniffing for a cron or cloud run | with no one present a point that needs judgment must deny harder, and magus is told where it runs rather than guessing             | [Told, never guessed](../../../doctrine.md#told-never-guessed); Record of refusals: detecting where magus runs; [ADR 0013](../../../decisions/0013-the-guard-denies.md) |
| Model escalation, retries on a bigger model, or a budget for long runs      | the tier table is static, and a person changes it                                                                                  | Record of refusals: a model router                                                                                                                                      |
| A daemon job that reruns, queues or merges on a provider unasked            | it acts on a provider with no one behind it, and reopens whether the maintenance scheduler opens workspaces it was not asked about | [ADR 0014](../../../decisions/0014-running-magus-is-the-consent.md), open questions                                                                                     |
| Auto-merge, admin merge, or any push a person did not see                   | the `merge` field stays a command handed over                                                                                      | [Human in the loop](../../../doctrine.md#human-in-the-loop)                                                                                                             |
| A hosted service for scheduling                                             | no capability requires one                                                                                                         | [The standard](../../../doctrine.md#the-standard), criterion 7                                                                                                          |
