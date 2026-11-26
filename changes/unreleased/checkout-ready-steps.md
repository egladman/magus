### Added

- **`checkout.ready` declares what a fresh job checkout runs.** A list of `{target,
  project}` steps in magus.yaml. `magus job fork --checkout` and `magus job exec` adoption
  run each as `./magus run <target> <project>` in the new checkout, read from that
  checkout's own magus.yaml, after its ./magus stands and is pinned, whether or not the
  workspace builds its own magus. The row's worktree records each step with its output ref.
  A failing step stops the rest and fails the fork or adoption with MGS3060, naming the
  step, its output ref and the line that reruns it. A workspace that declares none is told
  so once.
