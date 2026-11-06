### Fixed

- **The Pulls board reads every check state.** A cancelled check reads `CI cancelled`, not
  green, and a state outside the provider contract reads `CI unreadable` and names the
  check. Neither ranks the pull ready. An unknown mergeable or review value is named in
  the pull's blocked reasons instead of reading as unknown or none.
- **The Pulls poller keeps its rate-limit hold and backs off.** Changing the author filter
  no longer spends a hold for the rate limit's reset. A failed sync waits twice as long
  as the last one, from the poll floor up to 30 minutes, and a shutdown no longer records
  a failure.
- **An unresolvable Pulls state dir stops the server.** It used to log a warning and
  leave the Pulls app unmounted.
