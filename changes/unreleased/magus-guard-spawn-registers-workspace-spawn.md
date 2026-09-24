### Added

- **`magus\guard.spawn` registers a workspace spawn rule.** One Buzz function sees every
  subagent spawn and continuation and may deny or advise, never lift a built-in deny. An
  uncommitted loosening waits for a commit. Policy changes land on the trail as
  `guard_policy`; MGS1045 refuses a bad registration. magus ships no rule.
