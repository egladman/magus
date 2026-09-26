### Added

- **A `merge-queue: <method>` label queues any pull request.** On a pull request outside
  a stack, applied by someone with write access, it is merge intent like auto-merge,
  which GitHub will not enable on a pull request it says conflicts. The queue then
  resolves the conflict or kicks it back naming the files.
