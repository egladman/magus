---
title: Verdicts are provenance-blind; disposition is not
order: 8
description: No gate conditions on who or what wrote a change; accepting, sending and landing one carries a true name.
tags: [design, principle, agents, review]
---

# Verdicts are provenance-blind; disposition is not

A verdict (the cache saying a replay is honest, the drift gate saying
generated output matches source, a diagnostic, a CI result) judges the work.
No verdict conditions on who or what produced the change: cache keys hash
content, the drift gate compares bytes, diagnostics read the tree. Whether a
change was typed by hand or generated is invisible to every gate, on purpose,
because good software is the only defensible standard at the tool layer, and
a bar that moves with the byline is theater in both directions: extra
suspicion for one author, unearned trust for another.

Disposition is the opposite case. Accepting a proposal, permitting a finding,
sending a review, landing a change: these are accountability, and
accountability needs a true name. That is why a store magus keeps stamps
authorship from the transport a write arrived on, never from what the writer
claims about itself (the mechanism described under
[Agents propose, humans dispose](../judgment/agents-propose-humans-dispose.md)).
A commit's author is the checkout's configured name: it says who answers for
the commit, not what typed it.
The blindness half is structural today and stated here so that a proposal to
break it, a gate keyed on how code was written, has to argue with this page
first.

## Enforced by

- Cache keys hash content, the drift gate compares bytes, and diagnostics read
  the tree; none of them takes an author as input.
- The guard grades calls by who is acting, because every call it judges is an
  agent's. That decides who may act, never whether the work is good, and it
  reaches no cache key, no drift comparison and no diagnostic. A rule may key on
  the actor when no checker could judge the thing without knowing who made it:
  code can be judged by its bytes, while a note, a read receipt or a permitted
  finding says only that somebody decided. These rules are a seatbelt for a
  harness that opted in: CI sees the same bytes whoever wrote them.

## Good

The gate reads the bytes; the commit names who answers for them.

```sh
magus affected ci
git log -1 --format='%an <%ae>'
```

## Bad

```yaml
rules:
  - if: commit.trailers contains "Co-Authored-By: Claude"
    then: require_extra_review
```

The bar moves with the byline. A trailer is whatever the writer typed, so the
rule both insults honest work and waves through anything that leaves it out.

```yaml
rules:
  - if: session.agent != ""
    then: deny_write("**/*.go")
```

Code has a checker, so this judges the author in place of the work. "It is a
person's decision" does not stretch to cover it.

## See also

- [Agents propose, humans dispose](../judgment/agents-propose-humans-dispose.md)
- [Human in the loop](human-in-the-loop.md)
