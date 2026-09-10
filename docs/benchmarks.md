---
title: Benchmarks
description: "What magus measures about itself and publishes: the agent harness benchmark (does the agent surface make an agent cheaper or better on a real repository) and the build-tool comparison, with every run's environment stamped beside its numbers."
tags: [benchmarks, agents, harness, performance, measurement]
---

# Benchmarks

Two things are measured, and both results are committed with the environment
that produced them, so a number on this site can be traced to a date, a
version and a machine rather than remembered.

## The agent harness benchmark

The question is narrow: given the same model and the same task, does the magus
agent surface (skills, hooks, the routing index, the guard) make an agent
cheaper, faster, or more often correct than the same agent with only a
magusfile? Two arms run every task, `rampant` with the bare workspace and
`full` with everything `magus agent install` ships. The agent is the same in
both, and the only variable is what magus puts in the workspace.

Tasks come from two sources. [SWE-bench Verified](https://huggingface.co/datasets/SWE-bench/SWE-bench_Verified),
the human-validated set of five hundred real GitHub issues, is run inside each
instance's own container with a magusfile added, graded by the instance's own
tests. A small set of tasks on an enriched monorepo fixture covers what
single-package repositories cannot reach: cross-project affected sets and
generated-output drift.

Every task is checked by an exit code, never by the agent's own claim, and two
controls run before any number is trusted: the golden control applies the
known solution and must pass, the null control changes nothing and must fail.
A report's first table says whether both held. The statistics are the
community's: paired differences with a seeded bootstrap interval and a Holm
adjustment across tasks, Wilson intervals on pass rates, pass^k for
reliability, and cost-of-pass, the expected dollars per correct solution.

Published runs:

- [2026-09-10, Sonnet 5](benchmarks/2026-09-10-sonnet-5.md), the pilot: one
  task, three reps per arm.
- [2026-09-10, Opus 5](benchmarks/2026-09-10-opus-5.md), the same grid under
  Opus 5.

Read a pilot for what it is. Three reps on one task cannot separate the arms on
correctness, and the reports say so in their caveats; what they can show is the
direction and size of the cost difference, and both models agree on it.

The runner, the task definitions, the grader and the analysis live under
[`benchmarks/agent`](https://github.com/egladman/magus/tree/main/benchmarks/agent)
with a README that walks through running a grid yourself.

## The build-tool comparison

[Build-tool benchmarks](benchmarks/build-tools.md) times magus against make on
one fixture with hyperfine, with turbo, nx, lage, moon and bazel pinned for the
matrix to come. The results carry their date, hardware and exact tool versions.
The scenarios are under
[`benchmarks`](https://github.com/egladman/magus/tree/main/benchmarks).
