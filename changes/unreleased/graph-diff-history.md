### Changed

- **Breaking: `magus graph diff` reports git history under `history`.** Author nodes,
  `authored` and `co_changes` edges, and changes only to churn or staleness attrs
  leave `nodes_added`, `edges_added` and `nodes_changed`. A one-sided history is a
  note, not a list. `StripVCSAttrs` is gone; `DiffGraphs` splits history itself.
- **`magus graph diff` text output prints edges.** It counts nodes that only moved
  line and edges of removed nodes; `-o json` lists them.
