### Changed

- **Breaking: the spawn-time rules are renamed.** `brief-command` is now `spawn-command`,
  `brief-plain-commands` is `spawn-plain-commands`, and `spawn-unbriefed`,
  `buzz-unbriefed` and `architecture-unbriefed` are `spawn-before-skill`,
  `buzz-before-skill` and `architecture-before-skill`. A `magus\guard.builtins` that
  still names an old one fails to load, naming the new one.
- **`magus job exec` ends by saying to read the spec** (`magus describe job <id>`) rather
  than the terms, and `magus job fork` and `magus describe job` say spec too.
- **Breaking: `magus job fork` and `magus job apply` refuse a new job with no criteria or
  no model** (MGS3037). The row is the spec its holder reads, so its model now decides
  its tier and a workspace's model tier matrix is no longer asked at fork.
