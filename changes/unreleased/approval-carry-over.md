### Added

- **An approval carries over a push the classifier finds safe.** When approvals sit at an
  older commit, the merge queue replays the approved diff onto the head's base and
  classifies each path the head changes beyond it: `rebase`, `generated`, `prose`,
  `comment-only` or `code`. The approval carries when `queue.carry_approvals` in
  `magus.yaml` allows every tier (default: all but `code`).
