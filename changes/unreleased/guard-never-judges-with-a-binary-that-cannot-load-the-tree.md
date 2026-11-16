### Fixed

- **The hook glue never judges with a magus that cannot load the tree.** A binary that
  fails MGS1040 or denies under `stale-binary` is passed over for the next checkout's
  ./magus, then PATH's, and the notice names each. When none loads, the `go -C` bootstrap
  the refusal names still runs.
