### Changed

- **Breaking for SDK callers: `types.Observer` has one method, `Observe(GraphEvent)`.** It replaces `OnBuild`, `OnQuery` and `OnError`; `GraphEvent` replaces `BuildStats` and `QueryEvent`, `Op` is `GraphOpBuild` for a build, and `Err` marks a failure. A `graph.error` report record now names its `op`.
