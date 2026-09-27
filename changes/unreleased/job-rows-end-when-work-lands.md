### Changed

- **A job whose work landed on the base ends by itself.** An exited job's changed paths
  are compared offline at its checkout's HEAD and on the base branch; when all match, it
  ends as `no_return` naming the base commit. A squash of several jobs' branches ends
  each of them.
