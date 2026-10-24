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

Disposition is the opposite case. Accepting a proposal, sending a review,
landing a change: these are accountability, and accountability needs a true
name. That is why authorship is stamped from the transport a write arrived
on, never from what the writer claims about itself (the mechanism described
under [Agents propose, humans dispose](../judgment/agents-propose-humans-dispose.md)).
The blindness half is structural today and stated here so that a proposal to
break it, a gate keyed on how code was written, has to argue with this page
first.

## Enforced by

- Cache keys hash content, the drift gate compares bytes, and diagnostics read
  the tree; none of them takes an author as input.
- Exactly one code path grades a write by who is acting, and naming it keeps
  the rule checkable. `gradeLeasedWrite` reads the lease ledger to decide
  whether a worker is editing outside the paths its lease declared. That is a
  concurrency-ownership question in the guard (who owns this file right now)
  rather than a judgment about the work, and it reaches no cache key, no drift
  comparison, and no diagnostic. Its uncertainties (no ledger, no live lease, a
  file that will not parse) fail open with at most an advisory.

## Good

The gate reads the bytes; the record of who landed them reads the transport.

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

## See also

- [Agents propose, humans dispose](../judgment/agents-propose-humans-dispose.md)
- [Human in the loop](human-in-the-loop.md)
