### Fixed

- **Job usage stays unknown when a call reported no context size, billing is not metered, or the model has no row in the model table.** A lens row's `mean_cost_usd` and `median_tokens` are `null` unless every job in the row carries the figure.
