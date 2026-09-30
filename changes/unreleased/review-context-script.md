### Added

- **`hack/show-review-context.buzz`** turns a change into the per-symbol context a reviewer
  needs: each touched symbol's signature, exposure and path to the API, callers, callees,
  neighbouring members, tests, cited diagnostics and notes, plus the projects, dependencies,
  co-change, owners and vocabulary the change moves. Every section reads found, absent or
  unknown. It reads `--rev`, `--patch` (stdin with `-`) or a saved `--from` diff, and prints
  markdown or `-o json`. `magus diff --prompt` points at it for per-symbol depth.
