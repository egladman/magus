### Fixed

- **`magus vcs resolve --against` names the backend's own way to conclude a clean merge.**
  It printed `git commit` on every backend. git now reads `git merge --continue`, hg and
  Sapling a commit, and jj says the merge is already the working-copy commit.
