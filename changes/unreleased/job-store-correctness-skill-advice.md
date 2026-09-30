### Fixed

- **The guard's "installed skill" advice no longer fires on a skill's source.** It reads
  the `source: magus` stamp from the frontmatter only, so editing
  `internal/agent/skills/*/SKILL.md` is not mistaken for editing an installed copy.
