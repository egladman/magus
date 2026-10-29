### Added

- **Measured conformance thresholds.** `knowledge.conformance` in `magus.yaml` sets, per metric and population, the anchor a check enforces and the percentile a proposal measures at. A check enforces the anchor, never a live percentile.
- **`magus\conformance(checks, base:)` returns the conformance ceilings a change crossed:** each file changed since the base whose count of violations of a metric in `checks` rose past its count there. The base is measured under the workspace's policy and kept in the user cache per commit; no count is stored in the workspace.
- **`magus doctor` checks `conformance-ceiling`**, naming each configured anchor the code now measures tighter than. It edits nothing.
