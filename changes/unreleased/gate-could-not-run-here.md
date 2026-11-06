### Fixed

- **A gate that only lacked a tool records "could not run here", not a failure.** When
  every failed target is MGS3003 or MGS3004, such as a stopped docker daemon, the gate
  verdict is `unrun` and names each target. The run still exits non-zero. The push guard
  lets that commit out and names what PR CI owes.
