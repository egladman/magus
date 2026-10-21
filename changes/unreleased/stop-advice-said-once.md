### Fixed

- **The guard advises a root session once per lingering background task.** The
  `background-left` advice named every task older than 30 minutes after every turn, and a
  task whose end never reached the transcript was named for the rest of the session. Each
  task is now named once per session, with the `TaskStop` call that stops it.
