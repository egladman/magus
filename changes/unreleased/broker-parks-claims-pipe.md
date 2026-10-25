### Changed

- **A downstream pipe stage waits on its upstream's exit, not a timer.** It blocks on
  kqueue on macOS and on a pidfd on Linux 5.3 and later; an older Linux kernel still
  checks every 100ms and says so once.
