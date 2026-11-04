### Fixed

- **The mod's Copy fix button says when nothing was copied.** It read success from the
  call and never checked it, so a remote client or a missing clipboard still toasted
  `Copied`. It now reports the reason and the command to run by hand.
