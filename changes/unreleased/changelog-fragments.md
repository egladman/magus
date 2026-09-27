### Changed

- **The changelog is fragments under `changes/unreleased/` plus the release
  manifests, and the root `CHANGELOG.md` is gone.** One file per entry, so concurrent
  pull requests never edit one shared section; the docs changelog page renders both.
  A malformed fragment or an unknown group fails `pr-changelog`.
