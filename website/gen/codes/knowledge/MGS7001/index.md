---
title: "MGS7001: unresolvable buzz import"
description: Fires when the knowledge-graph extractor cannot resolve a workspace-relative buzz import to a scanned file, so the import becomes an inferred edge to the literal path instead of a resolved file-to-file edge.
tags: [MGS7001, knowledge graph, buzz, import, extraction, inferred]
---

# MGS7001: unresolvable buzz import

The knowledge-graph builder walks every `.buzz` source and records an
`imports` edge per import statement. When an import path resolves to a scanned
workspace file, the edge is EXTRACTED (confidence 1.0). When it does not, the
builder keeps the import as an INFERRED edge to a literal `import:<path>` node
and tags that node with this code, so the ambiguity stays visible instead of
being silently dropped.

Built-in `magus` and `magus/*` imports are stdlib modules, not files; they are
expected to be unresolvable and are NOT flagged. This code is reserved for a
path that looks workspace-relative (e.g. `spells/foo`) but matches no scanned
`.buzz` file.

## Why

The graph is deterministic and derived: every edge is rebuildable from
workspace sources. An import that points nowhere is not an error in the build -
the buzz program may still run (the file could be generated later, or the
import could be dead) - but it is genuinely ambiguous for graph purposes,
because there is no file node to link to. Recording it as an inferred edge to
the literal, tagged with this code, keeps the graph honest: an agent querying
the import node sees that magus could not resolve it, rather than assuming a
resolved dependency that does not exist.

## Resolution

- If the import is a typo or points at a moved file, fix the path so it names a
  real `.buzz` source; the edge upgrades to extracted on the next build.
- If the import is intentionally dynamic or points at a generated file, no
  action is needed - the inferred edge is the correct representation.
- To find every flagged import: `magus query kind:import`, then `magus explain`
  the ones whose `diagnostic` attribute is `MGS7001`.
