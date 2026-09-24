### Added

- **Spells can be imported from a registry by path.** `import "ghcr.io/team/spells/lint";`
  is declared with a tag in `magus.yaml` and pinned in `magus.lock`; only the `update`
  charm, through `magus spell lock --update`, resolves a tag. A `path:` entry replaces an
  embedded or remote spell. MGS1041 through MGS1044 cover undeclared, stale, mismatched
  and invalid. `magus spell build|push|pull|ls` publish.
