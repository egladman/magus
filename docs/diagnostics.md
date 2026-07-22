---
title: Diagnostics
description: Every magus error is a pointable coded diagnostic (MGSxxxx) with a handwritten resolution page and a queryable graph node, written for a human to act on rather than a machine to parse - the "is this my problem?" tax, absorbed by the tool.
tags: [diagnostics, errors, codes, MGS, DX, drift, actionable]
---

# Diagnostics

Most build tools hand you a wall of text and leave you to reason out whether the
failure is your change, your environment, or a bug in the tool. magus treats that
reasoning as the tool's job, not yours. Every diagnostic magus emits is a **pointable
coded error** with a name, a cause, and a resolution - designed for a human to act on,
not for a machine to parse.

## Anatomy of a diagnostic

```text
[MGS4005] generated output drifted but its declared inputs are unchanged; the committed
form is produced by the pinned release and you are running a dev build (v0.1.0-5-gabc123)
- not your change, do not commit (see: https://.../codes/race/MGS4005.md)
```

Three things come with every code, for free:

- **A stable code (`MGSxxxx`)** you can search, grep your CI logs for, or paste to a
  teammate. Codes never move once assigned.
- **A handwritten resolution page** at the `see:` URL - a real Why and Resolution
  written by a person, so neither you nor an agent has to reverse-engineer the fix.
- **A queryable graph node.** Every code is a node in the [knowledge graph](knowledge.md):
  `magus explain MGS4005` prints the card, and the code page is a first-class doc-site
  page. Nothing is ad-hoc free text.

## Codes are grouped into families

The prefix tells you the domain at a glance:

| Range | Domain |
|-------|--------|
| MGS1xxx | magusfile authoring |
| MGS2xxx | sandbox / permissions |
| MGS3xxx | workspace scope |
| MGS4xxx | determinism and drift |
| MGS5xxx | services |
| MGS6xxx | charms |
| MGS7xxx | knowledge-graph extraction |
| MGS8xxx | output references |
| MGS9xxx | auth / connectors |

## A worked example: drift classification

The clearest expression of the philosophy is what magus does when a generated file
drifts. A `generate` gate re-runs the generators and checks whether the tree went
dirty. When it did, `vcs.classifyDrift` names *why*, instead of just failing:

- **[MGS4006](codes/race/MGS4006.md) - stale generated output.** A declared input
  actually changed. Real drift: regenerate and commit.
- **[MGS4005](codes/race/MGS4005.md) - environmental drift.** The declared inputs are
  byte-identical to what is committed, but a dev build (or a locally installed tool at a
  different version than the pinned release) rendered them differently - the classic
  markdown-emphasis case, `*x*` versus `_x_`. **Not your change; do not commit.**
- **[MGS4003](codes/race/MGS4003.md) - non-deterministic output.** Same inputs, same
  generator version, output still differs: a reproducibility bug in the generator.

magus already holds every fact to make that call - a content hash of the declared
inputs, a version fingerprint of the generator and tools, and the produces/consumes
edges in the graph - so it makes the call for you. An agent no longer reads a 25-file
diff and reasons about markdown emphasis to conclude "toolchain noise, ignore." The code
says it.

## Why this matters

The same diagnostic serves three audiences with no extra work:

- **A human** gets a named cause and a linked resolution instead of a hunt.
- **An agent** gets a stable code it can branch on, so it stops burning context
  re-deriving "is this my problem?" every session.
- **CI** gets an identical, coded signal - the drift gate on a clean checkout with the
  pinned release stays a true content-drift gate, now with a code attached.

This is deliberate design: the cost of diagnosing a failure should be paid once, in the
tool, not re-paid by every person and every agent that hits it.
