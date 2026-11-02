### Changed

- **Protobuf board RPC is `GetHistoryMetrics`.** Package
  `magus.historymetrics.v1alpha1` replaces `magus.insight.v1alpha1`. Nested
  messages match the domain nouns; only the RPC Request/Response use those suffixes.
  Domain and protobuf stay separate; handlers still map fields.
