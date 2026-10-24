---
title: Manual on purpose
order: 10
description: Automate a repetition, never a rep; each act that forms judgment stays a person's keystroke.
tags: [design, judgment, agents, review]
---

# Manual on purpose

magus could automate each row below, and does not. Each is a place the
[human in the loop](../principles/human-in-the-loop.md) stays one.

| stays manual                | because                                                                                                                         |
| --------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| disposing an agent request  | the request stays open until a person has seen what it names; a repeat does not ask again, and nothing here answers it for them |
| applying suggested changes  | a suggestion lands only when a person accepts it                                                                                |
| writing the knowledge store | notes are human-authored by construction: there is no author field to spoof, because authorship rides version control           |
| sending a review            | a remark reaches a colleague under your name; an agent drafts into the session and a person sends the batch                     |

All four would be cheap to build, and cheap is not the test. The test for any
future row: does automating the step remove a repetition, or remove a rep? A
repetition (the same build run again for no new information) is the tool's
to eat, and the cache exists to eat it. A rep (reading the failure, deciding
what it means, choosing what lands) is where judgment forms. When a workflow is
tuned so far that its operator no longer forms the judgment it depends on, it
has automated the wrong half. Each row above removes the person at the point
where the mechanism needs their judgment, so each stays out.

[Sending a review](../../concepts/review.md) is the newest of them and the one with
the most to give away, so its refusals are worth naming: no review
command carries a `--publish` flag, a self-review is always a comment rather
than an approval, and authorship is stamped from the transport a write arrived
on rather than from what the writer claims. A batch waits for a person because
publishing is one outward-facing act. A call per remark would turn the one act a
person confirms into a series of small ones nobody does.

## Enforced by

- No review command carries a `--publish` flag.
- `session dispose` takes a typed id.
- Notes have no author field; authorship rides version control.

## Good

The cache eats the repetition:

```sh
magus run test .
magus run test .
```

The second run replays; nothing new was learned by running it again.

## Bad

```sh
magus session dispose --all --yes
```

magus has no such flag, on purpose. It would answer every open request without
reading one, which is the rep the request existed to make someone do.

## See also

- [Human in the loop](../principles/human-in-the-loop.md)
- [The loop keeps you able to notice](the-loop-keeps-you-able-to-notice.md)
- [Review](../../concepts/review.md)
