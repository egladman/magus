### Fixed

- **The `output-pipe`/`output-redirect` exemption for `magus query output` and
  `magus refs --text` now sees past a global flag.** It anchored on the first argument
  after `magus`, so `magus --root <dir> query output <ref> | grep x` was wrongly denied;
  the check now reads argv the same way the read-ack rule does, ignoring where a global
  flag sits.
