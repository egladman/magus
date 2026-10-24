### Added

- **`magus session --brief` reports the open attention queue.** It prints the open count
  `session ls` shows, the request that has waited longest and what raised it, and the
  requests raised since the checkout's previous host session, capped at five. A store it
  cannot read prints why instead of reading as an empty queue.
