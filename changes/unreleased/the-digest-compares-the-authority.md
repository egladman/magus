### Fixed

- **A worker's binary is judged against its authority, not the root's tree.** The
  contract digest of a leased call compares the judging binary with the files at its
  row's `Authority` revision, so the root relinking or moving its working tree no longer
  refuses every state-changing call of a live worker as stale-binary. A call with no
  lease still compares with its checkout's working tree. Version control gains an
  optional capability to list a revision's files under a directory, in git, Mercurial,
  Sapling and Jujutsu.
  [ADR 0045](https://eli.gladman.cc/magus/decisions/0045-a-worker-is-judged-by-its-checkpoint/).
