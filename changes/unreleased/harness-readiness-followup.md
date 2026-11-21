### Fixed

- OpenCode harness verification stops checking installed copies of managed hook
  helpers. The running binary supplies those scripts, including when checkout
  bootstrap links the hooks directory to its authored sources.
- The development watcher runs the `go-build` target for each rebuild, leaving
  that target as the sole owner of the installed OpenCode wasm output.
