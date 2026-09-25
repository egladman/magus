### Fixed

- **The merge queue no longer kicks a change back for a red it inherited.** A red
  candidate whose base is red on the same projects waits with `WAIT_BASE_RED`, keeps
  its place and gets no comment. The base is gated once a run, however many changes it
  reddens.
