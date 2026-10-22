### Security

- **The agent guard closes four ways around its rules.** A shell `command` opening with
  `*** Begin Patch` is judged as shell; a wrapped push keeps the branch `push-ungated`
  reads; `hook-wiring-write` judges shell writes to `.claude/settings*.json` and covers
  `./magus`; a client script `path` is read inside the workspace root, as the daemon reads
  it. Branch-name advice quotes what the shell would expand.
