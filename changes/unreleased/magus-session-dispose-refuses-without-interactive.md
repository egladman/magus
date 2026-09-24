### Fixed

- **`magus session dispose` refuses without an interactive terminal and is denied to
  agents.** Disposing an attention request records that a PERSON answered it. Outside a
  terminal the CLI exits 2 with the `--ack` sentence, and the guard rule `agent-sign-off`
  (widened from `read-ack`) denies every spelling on every agent channel.
