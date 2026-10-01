### Fixed

- **A call to a member an untyped value lacks names the member and the line.** It raised
  `null is not callable` with no position; it now raises
  `<file>:<line>: unknown method push on list`.
