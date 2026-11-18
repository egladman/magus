### Fixed

- **An advise at `Stop` never holds the turn open.** Claude Code continues a
  turn to deliver a stop's `additionalContext`, so the advice goes to
  `systemMessage`. A workspace stop rule's deny passes as advice on a held
  stop, and on a root stop over a background spawn the host tracks.
