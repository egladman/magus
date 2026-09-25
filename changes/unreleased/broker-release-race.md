### Fixed

- **A claim released while its holder reconnects no longer stays on the new broker.** A
  release that landed after the new broker recorded the re-asserted claim, but before
  the client finished reconnecting, was dropped, so the broker counted that memory
  until the holder's process exited. The client now hands such a claim back as soon
  as it reconnects.
