### Changed

- **A planted token record cannot outrank a minted one.** The store skips, with MGS9019,
  a record holding `tokens=write`, outliving 366 days or naming another file, and keeps
  the rest; `magus doctor` fails on it. Revoke takes an exact id or name, within the
  caller's grant. Every mint is audited, and a revoked token ends its open streams.
