---
title: Agents propose, humans dispose
order: 1
description: An agent integration can suggest work but never accept it; authorship comes from the transport, never the writer's claim.
tags: [design, judgment, agents, review]
---

# Agents propose, humans dispose

An agent integration can suggest work; it cannot accept it. That is
[human in the loop](../principles/human-in-the-loop.md) at the point where a
proposal becomes a change. magus records authorship from the transport that
performed the write, so a change carries the origin the channel delivered (the
OS account, the entry point, the credential or host) no matter what the writer
reports about itself. Interrupting a person costs attention, and the suggestion
operation reflects that: it requires a stated reason before the proposal
reaches anyone.

The job store is ordinarily written by agents, unlike the attention events
`notify` raises, because an attention event is addressed to a person while a
job is an agent-to-agent declaration read back by the guard and the console. A
person still has a full door onto it: `magus job` forks, takes and returns a
job, and `magus ls jobs` reads the plan back.

Automated review is wrong at a steady rate, and wrong in a characteristic way:
the confident finding that "fixes" behavior somebody chose on purpose. The
person deciding whether a proposal lands is the one who catches those findings.
If magus applied them itself, they would ship unexamined.

## Enforced by

- Authorship is stamped from the transport a write arrived on.
- The suggestion operation refuses a proposal with no stated reason.
- One author per job row, enforced by the store rather than by a closed CLI.

## Good

The worker returns its result; a person or the root verifies it before
anything lands:

```sh
magus job exit design-index --stdin
magus job wait design-index
```

## Bad

```sh
review-bot --apply-suggestions --push
```

The bot's confident findings land with nobody reading them, including the one
that "fixes" behavior somebody chose on purpose.

## See also

- [Human in the loop](../principles/human-in-the-loop.md)
- [Verdicts are provenance-blind; disposition is not](../principles/verdicts-are-provenance-blind.md)
- [Manual on purpose](manual-on-purpose.md)
