### Added

- **`hack/dev/prune-changelog-fragments.buzz` proposes which unreleased fragments to drop.**
  A fragment is superseded, obsolete or a duplicate only when blame and the symbol index
  prove it: its commit's lines are gone at HEAD, or two commits own lines in the same
  symbols. Anything unproven is kept with the reason. It writes only with `--apply`.
