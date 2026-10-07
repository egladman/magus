### Fixed

- **`lease-vcs` graded scratch repositories.** A git commit, merge, reset or worktree
  removal in a repository that shares no git directory with the workspace and lies
  outside its checkouts now runs under a worker lease, so tests that build throwaway
  repositories pass whatever lease their caller holds. A push is still refused.
