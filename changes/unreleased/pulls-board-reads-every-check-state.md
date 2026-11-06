### Fixed

- **The Pulls board reads every check state.** A cancelled check reads `CI cancelled`, not
  green, and a state outside the provider contract reads `CI unreadable` and names the
  check. Neither ranks the pull ready. An unknown mergeable or review value is named in
  the pull's blocked reasons instead of reading as unknown or none.
