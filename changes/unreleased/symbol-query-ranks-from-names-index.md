### Changed

- **A symbol query ranks from a names index.** `query --kind symbol` used to decode every
  symbol shard. It now ranks matches from a compact index of node names and decodes only
  the shards its answer's neighborhood touches, with the same output.
