---
title: "ADR 0017: a cache hit stays in its checkout"
order: 17
description: A cache hit from another checkout of the same repository used to be adopted into this checkout's store as if this checkout had run it. This record keeps every hit in the checkout that asked for it, marks it cached, names the producing checkout in the manifest, and says on stderr or in an error when a replayed log names paths outside the checkout that replays it.
tags: [adr, decision, cache, checkouts, worktrees, outputs, diagnostics]
status: accepted
date: 2026-10-24
---

# ADR 0017: a cache hit stays in its checkout

## Context

Every worktree and clone of one repository shares its invocation history, and a target
run in one checkout can be reused by another. Before this record, a hit that came from
another checkout was adopted into the asking checkout's output store as if that checkout
had run the target. Its attempt history, its manifest and its output paths all named a
run that never happened there.

The forces, measured in this tree on 2026-10-24:

- The shared golangci-lint cache holds 10,202 paths from 5 checkouts. A replayed lint log
  names paths under the checkout that wrote it, so a lint replay in a sibling checkout
  reports findings for files that checkout does not have, or points at a path that exists
  only there.
- 946 of 13,742 stored output logs were recorded by 134 other checkouts. Any lookup that
  reads another checkout's store is therefore a common case, not an edge case.
- A log that names a path outside the checkout that replays it is wrong for the reader,
  but a log that names a path inside it is the same log. The two need different answers.

The output store already falls back to other checkouts on a miss
([MGS8001](../reference/codes/outputref/MGS8001.md) names the checkout that ran a ref).
What was missing was a record of where a hit came from, and a rule for what a replay may
claim about this checkout.

## Decision

### A hit is a new attempt, marked cached

A hit writes a new attempt in the asking checkout's store, with `OutputDescriptor.Cached`
set. The attempt names the output that answered; the run that produced it is not copied
over as this checkout's own run. `StepRef` and `AdoptImported` are deleted: nothing
adopts another checkout's attempt into this one.

### The manifest records the producing checkout

`Manifest.Checkout` records the producer's root, an absolute host path. The path travels
to the remote tier with the manifest. That is accepted: the root is the only way a reader
of a replayed log finds the checkout it was written in.

### A replayed log that names paths is checked against this checkout

When a replayed log names `n` paths, magus counts how many lie under the producing
checkout and how many lie outside this checkout:

| Case | Output | Run |
| --- | --- | --- |
| Paths under the producing checkout | notice: `<project>:<target> replayed a log recorded in another checkout, <producer>; it names <n> path(s) under that checkout` | succeeds |
| Paths outside this checkout | error: `<project>:<target> replayed a log recorded in another checkout, <producer>; it names <n> path(s) outside this checkout, <here>` | succeeds; the error reports |

The error reports but does not fail the run. Eli decided this on 2026-10-24: a replay
that names foreign paths is a fact the person must see, and failing the run would hide
the rest of the result. The diagnostic code is
[MGS8005](../reference/codes/outputref/README.md).

### Each checkout caches its own tool results

Every tool result cache is scoped to its checkout through the sandbox environment, so one
checkout's lint or test cache cannot answer for another. Content-addressed caches
(`GOCACHE`, `GOMODCACHE`) stay shared: their keys already name the content, so sharing
them is safe. The guard refuses a direct `custom-gcl` run with an unscoped cache, which is
[MGS2014](../reference/codes/sandbox/README.md). `install-linter` clears the lint cache on
rebuild.

### Query output keeps its fallback and says where it read

`magus query output` keeps its search of other checkouts on a miss. When it answers from
another checkout, it says which one on stderr. The read is a notice, not an info line:
an agent under `MAGUS_LOG_SILENT` never sees an info line, so the read is raised to the
notice level (`readRef` in `checkouts.go`, pinned by
`TestOutputLookupsReadTheCheckoutThatRanTheRef`).

### Doctor reports a checkout-scoped cache it cannot scope

`magus doctor` reports a finding when a tool that needs a scoped cache runs with an
unscoped one, using the same condition as the MGS2014 guard deny.

### State

| Item | State |
| --- | --- |
| A hit writes a new attempt marked cached (`OutputDescriptor.Cached`) | done |
| `StepRef` and `AdoptImported` deleted | done |
| `Manifest.Checkout` records the producer root | done |
| MGS8005 notice and error text, reports without failing the run | done |
| `readRef` logs the cross-checkout read at notice level, pinned in `checkouts_test.go` | done |
| MGS2014 guard deny for a direct `custom-gcl` run with an unscoped cache | done |
| MGS2014 `magus doctor` finding | done |
| Per-checkout sandbox env scoping for tool result caches | decided; not checked in this record's tree |
| `install-linter` clears the lint cache on rebuild | decided; not checked in this record's tree |

### Doctrine checked

- [No silent defaults](../doctrine.md#no-silent-defaults): relied on. A replay from another
  checkout is never silent: a notice names the producer, and an error names the paths it
  cannot reach.
- [Built for the expensive era](../doctrine.md#built-for-the-expensive-era): relied on. A
  cheaper model that replays a log gets the checkout and the path count in the line it
  reads, not a judgment it must make.
- [Automation you can interrogate](../doctrine.md#automation-you-can-interrogate): relied
  on. `Manifest.Checkout` records where a hit came from, so a reader can check it.
- [A record of refusals](../doctrine.md#a-record-of-refusals): relied on. The MGS2014 deny
  follows [ADR 0013](0013-the-guard-denies.md): it names its remedy, a scoped cache.

## Alternatives

- **Adopt the other checkout's attempt into this store.** Lost: the attempt history would
  name a run this checkout never made, and `StepRef` and `AdoptImported` existed only to
  make that look true. Reopen with evidence that every reader of an adopted attempt
  checks which checkout ran it.
- **Fail the run when a replay names foreign paths.** Lost: the rest of the replayed
  result is still correct, and a failure hides it. Eli decided on the error that reports.
  Reopen if a foreign path has caused a wrong pass in a measured run.
- **Share every tool result cache across checkouts.** Lost: the 10,202 lint paths from 5
  checkouts show a shared lint cache answers with another checkout's files. Content-addressed
  caches are kept shared, since their keys name the content.
- **Keep the info line.** Lost: `MAGUS_LOG_SILENT` hides it from every agent, so the fact
  that the answer came from elsewhere reached no one.

## Consequences

- A replayed log can be read in a checkout other than the one that wrote it, with the
  producer named on stderr.
- `Manifest.Checkout` carries an absolute host path to the remote tier. Two checkouts at
  different paths get different manifests for the same content.
- A caller that scripted `StepRef` or `AdoptImported` breaks. magus is pre-1.0 and ships no
  alias.

## Open questions

- Whether `Manifest.Checkout` should carry a path relative to the repository root instead,
  so the remote tier does not carry host paths at all. Accepted as absolute for now.
- How many foreign-path errors a run tolerates before the error should become a failure.
  No threshold is set.
