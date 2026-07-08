---
title: knowledge-graph diagnostics
page_type: overview
description: Landing page for MGS7xxx diagnostics that flag ambiguities the knowledge-graph extractor hits while building the deterministic graph, such as a buzz import that resolves to no file or a doc that cites an unregistered diagnostic code.
tags: [knowledge graph, diagnostics, error codes, MGS7xxx, extraction, buzz, docs]
---

# Knowledge-graph diagnostics

Codes in the `MGS7xxx` range flag ambiguities the knowledge-graph extractor hits
while assembling the deterministic graph from workspace sources (the graph that
backs `magus query`, `magus explain`, and `magus graph`). Unlike most magus
diagnostics, these
are not raised as errors that stop a build: the graph is derived and safe to
rebuild implicitly, so an ambiguity is recorded as a silent attribute on the
affected node (visible via `magus explain`) rather than logged. They mark spots
where extraction could not resolve something cleanly, for a human to check.

## Codes

- [MGS7001](MGS7001.md): a buzz import resolves to no scanned file and is not a
  compiled-in module, so it becomes an inferred edge to the literal path.
- [MGS7002](MGS7002.md): a doc page cites an `MGS####` code that is not
  registered, so no edge can link the mention to a real diagnostic node.
