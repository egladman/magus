### Changed

- **Guard templates are at version 38.** `magus-command.buzz` sends a stop advise to
  `systemMessage`, so Claude Code never continues a turn for it. A 37 copy still sends
  `additionalContext`; `magus doctor` names the older copy.
