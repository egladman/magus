### Changed

- **The broker holds a waiting claim until a release seats it.** A step kept out only by
  its own run or its job tree's validation seat, and `magus status --wait`, now wait on
  one open connection instead of asking every 100ms. A smaller claim may pass the head
  of the queue four times. The broker protocol is now 3.
