### Fixed

- **The Pulls poller keeps its rate-limit hold and backs off.** Changing the author filter
  no longer spends a hold for the rate limit's reset. A failed sync waits twice as long
  as the last one, from the poll floor up to 30 minutes, and a shutdown no longer records
  a failure.
