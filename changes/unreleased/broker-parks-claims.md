### Changed

- **The broker holds a waiting claim instead of making it ask again.** A step kept out
  only by its own run, or by its job tree's validation seat, now waits on its open
  connection and is seated when a release makes room, where it used to ask every 100ms.
  `magus status --wait` is one held request too, bounded by its deadline, instead of a
  fresh connection every 500ms. Waiters are served first come, first served; a smaller
  claim that fits the freed room may go first, at most four times before the head of the
  queue is seated. A claim held out by another invocation is still refused at once with
  MGS3009. The broker's status lists what it holds waiting, with how long each has
  waited, and a broker that drains or stops refuses every waiter instead of leaving it
  hanging. The broker protocol is now 3: `magus broker stop` replaces an older broker.
- **A run downstream in a shell pipe waits on its upstream stages' exit, not a timer.**
  It blocks on kqueue on macOS and on a pidfd on Linux 5.3 and later, where it used to
  check every 100ms; an older Linux kernel still checks every 100ms and says so once.
