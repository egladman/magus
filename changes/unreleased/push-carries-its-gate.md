### Changed

- **Breaking: the guard refuses an ungated push or `gh pr create` from the orchestrator too.**
  It asked before, and two pull requests went out with no local gate. The refusal names
  `magus status --wait`, then `magus affected ci`. A memory claim larger than the machine
  now says which claim and how to narrow it.
