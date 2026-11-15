### Changed

- **`spawn-above-parent-tier` lets a child outrank its parent and names the cost.** The advice gives both tiers, each model's input and output price per million tokens (unknown when no `mgs_listModels` row gives one) and the alias at the parent's tier. Only a deny setting refuses. `magus job fork` follows the same setting, and off silences both.
