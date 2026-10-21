### Changed

- **The guard reads a root's role from its live rows, not its history.** A session is
  an orchestrator only while a row it handed out is live, and `magus job exit` on a row
  it took returns it. Integration is one class, from merge to commit. A root may rebuild
  a stale ./magus, and a repeated refusal shows its new remedy.
