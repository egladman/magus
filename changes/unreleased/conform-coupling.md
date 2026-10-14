### Added

- **`magus diff` reports a shared function its callers do not share.** The `shared-loop`
  conformance check, on by default as advice, names a function with a knob (a parameter its
  body branches on, or an options field or `With` function one caller package alone uses)
  whose body and callers are past the coupling rule, and a re-signed function two callers
  share that now branches on a parameter. Its remedies are a copy per caller with one line
  saying why, or a `mode-knobs` known entry saying why it stays shared.
- **Four coupling conform metrics.** `shared-body` (anchor 94), `shared-reach` (7),
  `caller-spread` (3) and the `mode-knobs` list (0). Measured on this tree, 7 of 731 functions
  that two or more other packages call carry a knob, 1 is past the relative rule
  (`run.Exec` in `internal/proc/run`), and none is past the anchors.

### Changed

- **Knowledge schema 20.** A symbol node carries `branch_idents`, the identifiers its body
  switches on or tests bare; graphs built before it rebuild.
