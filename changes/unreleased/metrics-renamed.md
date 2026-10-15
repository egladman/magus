### Changed

- **Breaking: the local cache counters fold into `magus.cache.reads`.** `magus.cache.hits`, `magus.cache.misses` and `magus.cache.errors` are its `result=hit`, `miss` and `error`, and an error now also counts a step refused before it started. `magus.cache.duration` carries `result` in place of `outcome`.
