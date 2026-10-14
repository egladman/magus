### Changed

- **Breaking: the local cache counters fold into `magus.cache.reads`.** `magus.cache.hits`,
  `magus.cache.misses` and `magus.cache.errors` are its `result=hit`, `miss` and `error`,
  and an error now also counts a step refused before it started. `magus.cache.duration`
  carries `result` in place of `outcome`.
- **Breaking: the remote cache counters fold into `magus.cache.remote.ops`.**
  `magus.cache.remote.hits`, `.misses`, `.stores` and `.errors` are its `outcome=hit`,
  `miss`, `stored` and `error`, beside `method`. A put the store already held,
  `outcome=exists`, was counted by none of them and is counted here.
- **Breaking: the Buzz session pool counters fold into `magus.buzz.session.pool.events`.**
  `magus.buzz.session.pool.reuse{outcome}` is `event=reused` or `cold`,
  `magus.buzz.session.pool.evictions{source}` is `event=evicted`, and a session returned to
  the pool counts as `event=returned`. `magus.buzz.session.pool.idle` is unchanged.
- **Breaking: `outcome` reads `ok` or `error` on every metric that carries it.**
  `magus.target.duration` and `magus.buzz.host.call.count` wrote `success` where Buzz
  execs, imports and MCP calls wrote `ok`. `magus.buzz.host.call.duration` now carries
  `outcome`.
- **Breaking for SDK callers: `types.Observer` has one method, `Observe(GraphEvent)`.**
  It replaces `OnBuild`, `OnQuery` and `OnError`; `GraphEvent` replaces `BuildStats` and
  `QueryEvent`, `Op` is `GraphOpBuild` for a build, and `Err` marks a failure. A
  `graph.error` report record now names its `op`.
- **Breaking for SDK callers: gopherbuzz's `PoolObserver` takes a kind, not a bool.**
  `SessionAcquire` takes a `SessionAcquireKind` (`SessionReused`, `SessionCold`) and
  `SessionRelease` a `SessionReleaseKind` (`SessionRetained`, `SessionEvicted`).

### Removed

- **Breaking: counters that restated a histogram's count.** Read the count of the histogram
  instead: `magus.graph.queries` of `magus.graph.query.duration`, `magus.target.runs` of
  `magus.target.duration`, `magus.mcp.tool.calls` of `magus.mcp.tool.duration`,
  `magus.buzz.host.call.count` of `magus.buzz.host.call.duration`,
  `magus.buzz.spell.builtins.count` of `magus.buzz.spell.builtins.warm`, and
  `magus.review.merged_while_reading` of its `.duration`.

### Fixed

- **The dashboard's Buzz session reuse counts only reused sessions.** It summed every
  checkout, cold ones included, while `session_pool_reuse` promises acquires served from an
  idle session.
