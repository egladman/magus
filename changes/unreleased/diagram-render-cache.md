### Changed

- **The diagram routes render each figure once per graph and lens.** A repeat request for an
  unchanged graph is served from a bounded in-process cache, and concurrent requests for the
  same figure share one render. The diagram listing reads the symbol index's manifest to
  report `indexed` instead of loading the symbol shards.
