### Added

- **`hack/show-review-context.buzz` gives a reviewer per-symbol context.** For each touched
  symbol it prints the signature, exposure and path to the API, callers, callees, tests,
  cited diagnostics and notes, plus the projects, owners and vocabulary the change moves.
  Each section reads found, absent or unknown. It reads `--rev`, `--patch` or `--from`, and
  prints markdown or `-o json`.
