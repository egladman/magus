### Fixed

- **Ending a job releases the checkout it bound.** A row taken with `magus job exec`
  from a plain shell binds its checkout. When someone other than the holder ended the
  row, the binding stayed, so every later fork into that checkout refused with "bound
  job".
