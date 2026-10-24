### Fixed

- **A subagent's stop ends the job it held.** When the guard lets a subagent stop, the
  job it was bound to ends with its session, so a resumed session can take it back with
  `magus job exec`. A brief line the guard could not judge now names why, as an unjudged
  run does.
