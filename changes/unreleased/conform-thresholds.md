### Added

- **Measured conformance thresholds with a committed ratchet.** `knowledge.conform` in
  `magus.yaml` sets, per metric and population, the percentile a proposal measures at and
  the anchor no committed value may exceed; `knowledge.conform.ratchet` names the committed
  record of measured values and the items known to sit past them, each with a reason. A
  check enforces the committed value, else the anchor, and never a live percentile.
- **`magus\conform()`** answers the lens: per row the distribution, the enforced value and
  where it came from, a tighter value to commit when the live percentile has fallen, and the
  items past the value. An index it cannot vouch for answers `unknown`, never a pass.
- **`magus doctor` checks `conform-ratchet`:** a committed value the lens can tighten, and a
  known entry that no longer violates, each named at its line. It edits nothing.
- **This repository fails lint on `file-lines`, `clone-tokens`, `export-scope` and
  `dep-fanout`** past the ratchet (`hack/lint/code-stays-within-the-conform-ratchet.buzz`),
  and on a known entry that no longer violates. Every other metric is printed as advice.

### Changed

- **Knowledge schema 18 through 21.** The conform metrics add attributes to symbol nodes,
  starting with a body shape and its token count, so a graph built before them rebuilds.
