### Fixed

- **A claim released while a restarted broker was taking it back is released there.** A
  step finishing during the moment its claim moved to the new broker left the claim
  counted, so the broker admitted less work until that process exited.
