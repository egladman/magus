### Changed

- **The root package's exports are held to an allow list.** Lint fails when `internal/commands/testdata/api-go.lock`
  lists a root-package name or method that `hack/lint/sdk-exports.jsonl` does not, so growing the
  public SDK takes a one-line reason, and a line for a removed export is reported as stale.
