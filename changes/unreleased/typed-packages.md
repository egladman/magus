### Added

- **The knowledge graph reads every manifest the shipped spells declare.** A
  `package.json`, `pyproject.toml` or `Cargo.toml` contributes `package:npm`,
  `package:python` or `package:cargo` nodes at the versions its lockfile resolves,
  beside the `package:gomod` nodes go.mod already gave. `explain` prints each
  package's documentation URL, derived from manager, name and version and never
  fetched. Workspaces that gain nodes rebuild the `@packages` shard once.
