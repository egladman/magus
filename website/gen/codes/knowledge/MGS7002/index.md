---
title: "MGS7002: dangling doc reference"
description: Fires when a documentation page mentions an MGS#### diagnostic code that is not registered, so the knowledge-graph extractor cannot link the mention to a real diagnostic node.
tags: [MGS7002, knowledge graph, docs, diagnostic, extraction, inferred]
---

# MGS7002: dangling doc reference

The knowledge-graph builder scans markdown docs and records an inferred
`documents` edge from a doc page to each `MGS####` diagnostic code it mentions
in its body. When the mentioned code is a registered diagnostic, the edge links
to a real diagnostic node. When it is not - a typo, a code that was removed, or
one that was never defined - there is no node to link to, so the builder drops
the edge (a dangling edge would corrupt the graph) and tags the doc node with
this code and the offending references.

## Why

The graph is deterministic and derived: an edge must have both endpoints. A doc
that references a code (`MGS` followed by four digits) that is not registered
cannot produce a valid edge, so silently emitting one would leave a target with
no node - exactly the kind of torn state the builder guarantees against.
Recording the dangling reference on the doc node instead keeps the mention
discoverable (the doc clearly meant to cite a code) without inventing a phantom
diagnostic.

This is inferred, not extracted: an `MGS####` string in prose is a heuristic
match, so a false mention (e.g. an example of a hypothetical code) is possible.
The code marks the doc for a human to check, not a hard failure.

## Resolution

- If the reference is a typo, fix it to name a registered code; the edge links
  on the next build.
- If the code was intentionally removed, update the doc to drop or reword the
  mention.
- To find flagged docs: `magus query kind:doc`, then `magus explain` the ones
  whose `diagnostic` attribute is `MGS7002` to see the `unknown_codes` list.
