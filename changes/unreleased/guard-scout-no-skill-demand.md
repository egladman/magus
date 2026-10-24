### Fixed

- **The guard no longer asks a read-only subagent to load a skill.** A subagent whose own
  call names a shipped read-only type, such as `magus-scout`, has no Skill tool even when
  the guard never saw its spawn, and the skill rules skip its writes too. A write
  outside the workspace, such as a scratchpad report, asks for no skill.
