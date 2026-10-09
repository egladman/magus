### Added

- **The knowledge graph records `implements` edges.** A symbol that implements an interface,
  or a method that implements an interface method, gets an edge to it, read from the
  implementation relationships a SCIP indexer records. Only interfaces the workspace defines
  get an edge, the same trade `calls` makes, and an indexer that records no implementation
  relationships yields none. `magus diff` uses the edge to place an interface before its
  implementations in the reading order.

### Changed

- **The knowledge schema is 17.** The new edge widens what an extractor produces, so a graph
  built at schema 16 is read as absent and rebuilt in full on the next `magus graph build`
  or server index pass. Nothing needs migrating, and a consumer that parses the old form
  parses the new one unchanged. Skills and generated output that stamp the schema version
  carry 17.
