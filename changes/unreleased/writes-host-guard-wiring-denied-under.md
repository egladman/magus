### Changed

- **Writes to host guard wiring are denied under a bound lease.** Every verdict names the
  lease it was graded under; an unknown lease id is a deny.
