### Added

- **`magus diff`'s reading order prints each step's changed signatures.** Under every step, a `signature:` line gives each changed symbol's declaration as the indexer rendered it, followed by `was` and the old declaration when `--baseline` shows the signature changed. A symbol the indexer rendered no declaration for says so.
