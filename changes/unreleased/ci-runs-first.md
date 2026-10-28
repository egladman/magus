### Changed

- **A gate goes first on the build budget.** While a claim of `magus affected ci` or
  `magus run ci`, or of a magus it runs, waits, no other run is seated in the room it
  waits for. The oldest gate waits for seats other invocations hold instead of failing
  part-way, prints what it waits for and who holds it, and gives up after 30 minutes
  with an MGS3009 refusal naming the bound. Every other run is still refused at once.
