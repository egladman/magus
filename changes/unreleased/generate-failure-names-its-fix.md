### Changed

- **CI's generate check names its two fixes.** A red generate check now says to run
  `magus run generate:rw .` and commit the result, or to queue the PR with
  `gh pr merge --auto --squash` so the merge queue regenerates in its own candidate. The
  retired `magus:regenerate:requested` and `magus:regenerate:done` labels leave the retired list.
