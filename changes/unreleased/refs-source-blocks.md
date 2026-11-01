### Added

- **`magus refs` prints numbered source blocks you can page.** `--occurrences` takes
  `--limit`, `--offset` and `--file`; each site's code comes from `--source`, `--context`,
  `--before`, `--after` or a `--lines=A:B` range counted from the match, with `--max-lines`
  and `--skip-lines` capping a block. A cut page prints the next command with its values
  shell-quoted. Generated and test sites are marked. `magus explain --source` prints a
  doc section's lines the same way.

### Removed

- **`hack/dev/show-code-context.buzz` is gone.** `magus refs --occurrences --context N`
  does its job.
