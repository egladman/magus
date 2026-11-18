### Changed

- **Guard templates are at version 38.** `magus-command.buzz` sends a stop advise to
  `systemMessage`, so Claude Code never continues a turn for it. A 37 copy still sends
  `additionalContext`; `magus doctor` names the older copy.

### Fixed

- **background-left lets a subagent's second stop through.** When the host marks a stop
  as following a refused one, the refusal passes as advice, as a workspace stop rule's
  does, rather than holding the stop open until the host overrides it.
- **The workspace stop rule tells a background spawn from a try it cannot see end.**
  `hack/policy/guard.buzz` passes the stop's background spawns into `StopFacts`, and a
  running try the host does not track says nothing wakes the session for it.
