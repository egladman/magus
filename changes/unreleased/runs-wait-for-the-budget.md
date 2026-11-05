### Changed

- **Breaking: runs wait for the build budget, and `magus status --wait` is gone.** A
  claim that can fit waits in the broker's queue up to the new `budget_wait` (default
  30m; `0` refuses at once), saying when it parks and when holders change; MGS3009 then
  names the holders. Reservations, the gate-only bound and the guard's pipe rules
  go. Broker protocol 5.
