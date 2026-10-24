---
title: Measured, not asserted
order: 24
description: A claim that something is better, or that a rule holds, needs an artifact someone else can check; a measurement that kills an idea is kept.
tags: [design, principle, evidence]
---

# Measured, not asserted

A design reading well proves less than it used to, so a claim needs an artifact
someone else can check: a benchmark committed with the hardware, the date and
the competing tool versions; a run journal; a count over transcripts with the
recipe to reproduce it. Weigh anything without one as experience, and say so in
the sentence that makes the claim.

Measurement cuts both ways, and the useful half is the one that says no. A
conformance check that fires on four deliberate designs out of four is noise; an
advisory that fires on 0.8% of edits, all of them matcher artifacts, is a toll.
The rows in [A record of refusals](../refusals.md) that record a measurement
killing an idea somebody wanted are the reason the rest of that list is worth
anything.

A measurement also says where the tool falls short. The same journals that
show what the cache saved show what it did not, and a page that only publishes
the good half is marketing.

## Enforced by

- [Benchmarks](../../benchmarks.md) publishes every run beside the environment
  that produced it, controls first.
- A conformance threshold is a relative bound with a committed ratchet
  ([`hack/policy/conformance.jsonl`](https://github.com/egladman/magus/blob/main/hack/policy/conformance.jsonl)), so a rule's floor is a number in the tree.
- [A record of refusals](../refusals.md) names the measurement and its date
  beside each refusal.

## Good

A refusal row that carries its measurement:

```text
an advisory when an agent edits a file it never looked up | not built: 0.8% of 3,560
first edits were never anchored, and all 27 cases read by hand were matcher artifacts
```

## Bad

```text
The graph makes agents 3x faster.
```

No run, no control, no date, no recipe. Nobody can check it, and it outlives
whatever it was ever true of.

## See also

- [A record of refusals](../refusals.md)
- [Benchmarks](../../benchmarks.md)
- [The standard](../standard.md)
