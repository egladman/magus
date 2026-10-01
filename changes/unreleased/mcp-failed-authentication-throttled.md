### Security

- **Failed authentication on `/mcp` is throttled.** Each caller, keyed by `Origin` or else
  its peer, may fail 20 times a second in bursts of 40, then gets `429` with
  `Retry-After` and the new code MGS9030 (too many failed authentications).
  A valid token is never throttled.
