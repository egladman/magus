### Changed

- **Breaking for SDK callers: `magus.Open` refuses a broker it would never use.**
  `WithBroker` given a nil client, or a client while `WithBrokerPolicy` or the workspace's
  `broker` setting resolves to `off`, is an error from `Open` rather than a client silently
  ignored. Pass one or the other.
