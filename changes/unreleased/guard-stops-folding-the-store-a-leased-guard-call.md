### Fixed

- **A leased guard call no longer reads the whole session store.** A release request
  now also sits on the holder's job row, where the guard reads it. The advice fires once
  per lease, so one worker's notice no longer silences another's in the same session.
