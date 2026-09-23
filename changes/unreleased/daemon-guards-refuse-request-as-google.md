### Changed

- **The daemon's guards refuse a request as `google.rpc.Status` JSON in the route's protocol.**
  A Connect service answers Connect's envelope; every other route, `/mcp` included,
  AIP-193's `{"error":{"code","message","status","details"}}`. Both carry the MGS code as a
  `google.rpc.ErrorInfo` reason and a `google.rpc.Help` link. A missing bearer token is now
  MGS9011, split from a rejected one (MGS9001). Host, loopback, share-device and
  console-file refusals gain MGS9007-9010.
