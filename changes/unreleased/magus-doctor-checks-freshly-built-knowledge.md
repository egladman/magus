### Changed

- **`magus doctor` checks a freshly built knowledge graph.** `graph-bounds` built nothing
  and passed when `gen/knowledge-graph.json` was absent; it now builds the graph in process
  and fails when the build does. The graph JSON is no longer committed.
