### Changed

- **Breaking: `root-context-budget`, `root-heavy-command` and `root-repeat` only advise.** The strict level sets them to advise, and a workspace that declares `deny` for one gets MGS1045 naming `orchestrator-groundwork`. `root-heavy-command` and `root-repeat` skip a session that already hands out work. Command, write and spawn requests carry the caller's `Orchestration`.
