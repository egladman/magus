---
title: The loop keeps you able to notice
order: 11
description: What reaches a person is the deviation, the batch and the absence, never a confirmation of a pattern they already trust.
tags: [design, judgment, agents, attention]
---

# The loop keeps you able to notice

Reaching a person is not the safeguard. A person asked to approve what the machine
has already handled stops evaluating and starts confirming. Mitchell, Ghosh, and
Passi call that approval fatigue.[^mitchell-2026] Bainbridge named the other half
forty years earlier: the more a system handles, the less practiced its supervisor,
and the moment that needs a person is the moment that person is least
ready.[^bainbridge-1983]

[Corrective competence](corrective-competence.md) is what remains when the agent
is gone. This is what has to be present while the agent is running. A run of
approvals can pass while the skill the exception needs has already left.

The test is the one [Manual on purpose](manual-on-purpose.md) uses. Does this
prompt ask the person to notice something they would not otherwise have seen,
or to confirm a pattern they have already learned to trust? The first is a rep.
The second is a toll, the same test
[friction](../principles/friction-that-carries-meaning.md) uses.

## Enforced by

- A waiting or permission event the queue already holds does not raise another
  desktop notification: the open row is the block, and a second toast asks for a
  yes on it.
- The console shows a permission's close control only after that row is
  opened, so the paths the event named, and the message, have been on screen.
- `session dispose` stays a typed id.
- A failure still notifies, because it is news and news does not enter the
  queue.

## Good

What raises a desktop notification, and what does not:

```text
a failure                                     -> notifies; it is news
a waiting or permission event already queued  -> no second toast; the open row is the block
```

## Bad

```text
Agent wants to run `magus run test .`. Allow? [y/N]
```

Asked a hundred times a day about a command that is always fine, the person
learns to press `y`, and presses it the one time it was not fine.

## See also

- [Corrective competence](corrective-competence.md)
- [Human in the loop](../principles/human-in-the-loop.md)

[^mitchell-2026]: Margaret Mitchell, Avijit Ghosh, and Samir Passi, "AI Agents Push Humans
    Out of the Loop", arXiv:2608.23642, 2026, <https://arxiv.org/abs/2608.23642>.
    Checked 2026-09-28.

[^bainbridge-1983]: Lisanne Bainbridge, "Ironies of Automation", Automatica 19(6),
    775-779, 1983, <https://doi.org/10.1016/0005-1098(83)90046-8>. Checked 2026-09-28.
