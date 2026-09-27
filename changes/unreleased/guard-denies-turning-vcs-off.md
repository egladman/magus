### Added

- **The agent guard refuses a leased or agent-attributed write that sets `vcs.enabled:
  false` in a magus.yaml tier this workspace reads.** Disabling VCS drops the guard's
  approval authority (HEAD of whatever VCS resolves) entirely, so no Buzz policy edit
  after that write is checked against an approved copy. Every other edit passes.
