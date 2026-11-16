### Changed

- **A target or script builds one symbol graph for `refs`.** `magus\graph().refs` reads the all-symbols graph its evaluation shares instead of merging shards per symbol, so one lone `refs` call pays a full merge. A scheduled target drops its graphs when its attempt ends; a read after that rebuilds and warns once.
