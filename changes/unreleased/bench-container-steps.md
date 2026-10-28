### Changed

- **The agent benchmark's container steps are Go.** `benchmarks/agent/internal/container`
  pins a task image by digest, refuses an emulated platform unless allowed, creates the
  container with CPU and memory limits over the Engine API, copies magus in, seeds one
  Go project per module, applies the patch ladder and runs the eval script. The four
  `swebench/*.sh` scripts are gone.
