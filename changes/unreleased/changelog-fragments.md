### Changed

- **Unreleased changelog entries are fragments under `changes/unreleased/`.** One file
  per entry, so concurrent pull requests never edit one shared section; `CHANGELOG.md`
  keeps an empty `[Unreleased]` and the docs changelog page renders the fragments. A
  malformed fragment or an unknown group fails `pr-changelog`.
