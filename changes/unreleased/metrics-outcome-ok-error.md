### Changed

- **Breaking: `outcome` reads `ok` or `error` on every metric that carries it.** `magus.target.duration` and `magus.buzz.host.call.count` wrote `success` where Buzz execs, imports and MCP calls wrote `ok`. `magus.buzz.host.call.duration` now carries `outcome`.
