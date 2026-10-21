### Changed

- **The guard reads a root's role from its live rows, not its history.** A session is
  an orchestrator only while a row it handed out is declared, running or exited. A root
  can take one row with `magus job exec` and returns on `magus job exit`. A loaded skill
  or a spawn with no row no longer counts.
- **Integration is one class for orchestrator-groundwork.** merge, rebase, pull, am,
  reset, stash, restore, checkout and `switch -c` join cherry-pick, add and commit.
- **A root holding a parentless row may run the gate.**
- **A stale ./magus no longer strands the root.** While ./magus is older than its
  sources, the root may run `./magus run go-build .` or the bootstrap alone on its line,
  even when the old binary still loads the tree.
- **A repeated refusal shows its new remedy.** A refusal repeated after the remedy or
  the binary changed prints in full instead of as "denied again".
- **When no spawn can pass, orchestrator-groundwork names `magus job exec <row>`** instead
  of telling the session to spawn a worker.
