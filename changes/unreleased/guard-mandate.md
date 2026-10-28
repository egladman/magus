### Added

- **`magus\guard.stop` and `magus\risk.assess` for workspace policy.** A magusfile
  can register a Buzz rule for the host's Stop event, and its deny blocks the stop
  through the existing hook. A Buzz script can read the proven risk tier of a
  change, here or in another checkout.
