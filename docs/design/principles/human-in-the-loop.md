---
title: Human in the loop
order: 9
description: An agent proposes and does bounded work; a person decides at named points, each explicit, reasoned and recorded.
tags: [design, principle, agents, judgment]
---

# Human in the loop

magus keeps a person in the loop, the industry's term for the arrangement and
the plainest account of what the tool is for. An agent proposes and does
bounded work: it reads, drafts, runs the checks, and edits the paths a job
hands it. A person decides at named points: what lands, what leaves the
machine, what spends real money, and what changes the rules the agent works
under. Where those points sit is the design. magus makes each one explicit,
gives it a reason, and records it, so a decision is something a person
made and can look up, not something that happened.

A point exists only where the person's judgment changes the outcome. Asking
for everything trains the person to confirm everything, which
[the loop that keeps you able to notice](../judgment/the-loop-keeps-you-able-to-notice.md)
names as the failure, and [friction](friction-that-carries-meaning.md) is the
test for any new point. This prevents two quiet failures that look
opposite: the agent that decides because nobody was asked, and the person who
approves because everybody was. A default that acts without saying so is a
third: a decision nobody made. [No silent defaults](../judgment/no-silent-defaults.md)
closes it.

## Enforced by

- The guard's `ask` verdict puts a push at a commit no gate covers, or a run of
  a `human_only` target, in front of the person with the cost or the gate state
  attached, and refuses a leased worker outright, because a worker has no one
  to ask.
- An agent's request for attention stays open until a person disposes of it.
- A model tier, a guard rule's grade and a merge intent change only when a
  person edits the row that holds them, and that row is in version control.
- The [judgment](../judgment/agents-propose-humans-dispose.md) pages list the
  points one by one, starting with
  [Agents propose, humans dispose](../judgment/agents-propose-humans-dispose.md)
  and [Manual on purpose](../judgment/manual-on-purpose.md).

## Good

A blocked agent waits until a person has read what it asks and disposes of it
by typing its id:

```sh
magus session dispose <id>
```

## Bad

```yaml
on: pull_request_review
jobs:
  merge:
    steps:
      - run: gh pr merge --auto --squash
```

Every approval now lands the change, so the approval stops being a decision
and becomes a keystroke the agent waits behind.

## See also

- [Agents propose, humans dispose](../judgment/agents-propose-humans-dispose.md)
- [Manual on purpose](../judgment/manual-on-purpose.md)
- [The loop keeps you able to notice](../judgment/the-loop-keeps-you-able-to-notice.md)
