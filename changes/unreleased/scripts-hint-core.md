### Added

- **`next` can carry workspace scripts.** A script the index lists for a situation is
  served as `magus buzz <path> -- <args>` with id `script-<name>`, its `{ref}`, `{rule}`
  and `{project}` filled in or the script withheld. A script declared read reaches
  reviewers and workers; a write script never does.
