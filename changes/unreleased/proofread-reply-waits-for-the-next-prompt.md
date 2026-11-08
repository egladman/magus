### Changed

- **On Claude Code, the reply check hands its findings over with your next message.**
  `proofread-reply.buzz` prints nothing at `Stop`, because Stop context starts a turn
  where the agent answers advice you never saw. It keeps the findings per session and
  prints them on the next `UserPromptSubmit`. Wire the same command under both events.
