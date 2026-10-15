### Added

- **Measured conformance thresholds with a committed ratchet.** `knowledge.conformance` in `magus.yaml` sets, per metric and population, the percentile a proposal measures at and the anchor no committed value may exceed; `knowledge.conformance.ratchet` names the committed measured values and the known items past them, each with a reason. A check enforces the committed value, else the anchor, never a live percentile.
