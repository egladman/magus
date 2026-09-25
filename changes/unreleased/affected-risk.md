### Added

- **`magus affected <target> --risk` sizes the gate to the change.** It sorts a change
  into trivial, mechanical, scoped or full with per-file evidence, and prints the
  reduced gate as magus commands, Go tests narrowed to the changed packages' reverse
  dependencies through the new `go-test-packages` op. `ci.risk_min_runs` and
  `ci.risk_window` prune targets with a long clean record.
