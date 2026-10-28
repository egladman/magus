### Added

- **`proc\exec` and `vcs\cmd` take `timeoutMs`, and `proc\exec` takes `detach`.** A call past
  its timeout has its process group killed and raises, whatever `allowFailure` says. A
  detached process starts in a session of its own and outlives the script; it takes no
  timeout, and a sandbox policy refuses it.
