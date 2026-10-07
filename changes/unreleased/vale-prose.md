### Added

- **Vale judges prose that magus extracts.** `spells/vale` runs Vale on documents built
  from comment spans the spells' comment syntax finds, function names from the symbol
  indexes, commit messages and pull request text. Each finding maps back to its file and
  line. The `commit-msg` hook now holds every branch to a short subject with no body.
