### Fixed

- **A worker's binary is judged against its authority, not the root's tree.** A leased
  call's contract digest reads the files at its row's `Authority`, so the root relinking
  no longer refuses a live worker as stale-binary. An unleased call still reads its
  working tree. git, Mercurial, Sapling and Jujutsu list a revision's files.
  [ADR 0045](https://eli.gladman.cc/magus/decisions/0045-a-worker-is-judged-by-its-checkpoint/).
