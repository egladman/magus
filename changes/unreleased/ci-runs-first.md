### Changed

- **A gate goes first on the build budget.** While a `magus affected ci` or `magus run ci`
  claim waits, no other run is seated in the room it waits for. The oldest gate waits
  instead of failing part-way, says who holds the seats, and gives up after 30 minutes
  with MGS3009.
