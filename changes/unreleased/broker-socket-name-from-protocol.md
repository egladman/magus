### Fixed

- **A systemd socket unit on the old broker path names the fix.** The broker socket's name
  now comes from the protocol version. A unit installed before it moved fails activation
  with `install the units magus server broker units prints for this build`.
