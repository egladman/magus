### Changed

- **Breaking: the Buzz session pool counters fold into `magus.buzz.session.pool.events`.** `magus.buzz.session.pool.reuse{outcome}` is `event=reused` or `cold`, `magus.buzz.session.pool.evictions{source}` is `event=evicted`, and a session returned to the pool counts as `event=returned`. `magus.buzz.session.pool.idle` is unchanged.
