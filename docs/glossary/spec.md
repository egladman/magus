---
title: Spec
order: 12
description: "The declared half of a job row: what the parent wrote and the store validated (criteria, checkpoint, write and read paths, the check, the goals, the model, the decisions already made and the evidence behind them). The child reads it with magus describe job, and a view renders it as Markdown at read time, never stored."
tags: [glossary, sessions-and-leases]
---

# Spec

The declared half of a job row: what the parent wrote and the store validated
(criteria, checkpoint, write and read paths, the check, the goals, the model,
the decisions already made and the evidence behind them). The child reads it
with `magus describe job`, and a view renders it as Markdown at read time,
never stored. A fork without criteria or a model is refused.

The spec is the parent's record; the `JobResult` the child files with
`magus job exit --stdin` is the child's. The spawn prompt is neither: it is the
parent's own words, and it names the row the child reads.

## Elsewhere

A Kubernetes object's `spec` is the desired state its author declares, beside a
`status` the system writes. A magus spec is the same half of a job row, kept in
the row rather than nested under a `spec` member.

## Example

```sh
magus describe job my-job
```

## See also

- [Job](job.md)
- [Lease](lease.md)
- [ADR 0043: job records are Buzz data](../decisions/0043-job-records-are-buzz-data.md)
