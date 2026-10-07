### Added

- **Vale judges prose that magus extracts.** The built-in `vale` spell and the `comments`
  host module back `prose`, `commit-messages` and `pr-description` targets; `pr-title`
  runs Vale's title rules too. Each finding maps back to its file and line. Every commit
  subject check shares one cap of 100 characters.
