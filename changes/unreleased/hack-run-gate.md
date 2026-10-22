### Added

- **`hack/dev/run-gate.buzz` runs the gate and says what failed.**
  It waits for the broker to have a seat, runs `magus affected ci` under
  `magus job exec --timeout`, and prints each failed member's output ref with the first
  failing lines of its log. The guard judges the script as the gate.
  `hack/dev/bootstrap-checkout.buzz` gains `--here` and `--restart`, and trusts the
  checkout's `mise.toml` when mise is on PATH. `hack/README.md` has a table of the
  `hack/dev` scripts by task.
