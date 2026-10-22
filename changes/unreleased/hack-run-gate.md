### Added

- **`hack/dev/run-gate.buzz` runs the gate and says what failed.** It waits for a broker seat, runs `magus affected ci` under `magus job exec --timeout`, and prints each failed member's output ref with the first failing lines of its log. `hack/dev/bootstrap-checkout.buzz` gains `--here` and `--restart`, and `hack/README.md` tables the scripts by task.
