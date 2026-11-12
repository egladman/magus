### Changed

- **`magus job pin <job>...`** records the content hash of the `./magus` in each job's
  checkout on every live row standing there. The integrator runs it in its landing
  checkout after it relinks, and the root after it rebuilds a job's checkout. The fork and
  `hack/dev/bootstrap-worktree.buzz` pin the binary they place.
- **`magus job advance <job>...`** moves each job's authority to the HEAD of the checkout
  it runs in. The root runs it after it merges into a job's checkout. The integrator runs
  it for its own row and each row its `integrates` names.
- **Breaking: the root's hook hands a call only to a `./magus` a live job row pins.** The
  row records the binary's path and `binary_digest`. Any other `./magus` is not run, and
  the session's binary judges the call. The bootstrap re-exec follows the same rule.
  `binary-digests.jsonl` beside the job store caches each hash by inode, size and mtime.
- **Breaking: a leased worker builds no `./magus`, in a checkout its job owns too.** Its
  relink, `go generate`, `magus run go-build .`, `go run ./cmd/magus` and checkout
  bootstrap are refused, since no row would pin the binary. It asks the root for a
  placement instead.
- **The integrator may advance an authority.** The job store refuses the advance to every
  other bound caller.
