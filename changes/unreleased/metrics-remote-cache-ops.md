### Changed

- **Breaking: the remote cache counters fold into `magus.cache.remote.ops`.** `magus.cache.remote.hits`, `.misses`, `.stores` and `.errors` are its `outcome=hit`, `miss`, `stored` and `error`, beside `method`. A put the store already held, `outcome=exists`, was counted by none of them and is counted here.
