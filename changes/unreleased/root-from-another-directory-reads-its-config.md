### Fixed

- **`--root` from another directory no longer layers that directory's `magus.yaml`.** A
  working directory's config applies only when it sits inside the workspace, so a run
  pointed at another checkout reads that checkout's sandbox passthrough, not the shell's.
